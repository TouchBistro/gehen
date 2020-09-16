package main

import (
	"flag"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/DataDog/datadog-go/statsd"
	"github.com/TouchBistro/gehen/awsecs"
	"github.com/TouchBistro/gehen/config"
	"github.com/TouchBistro/goutils/fatal"
	"github.com/getsentry/sentry-go"
	"github.com/pkg/errors"
)

const timeoutMins = 5 // deployment check timeout in minutes

type deployment struct {
	name string
	err  error
}

var (
	gitsha     string
	configPath string
)

func fetchRevisionSha(url string) (string, error) {
	resp, err := http.Get(url)
	if resp != nil {
		defer resp.Body.Close()
	}

	if err != nil {
		return "", errors.Errorf("Failed to HTTP GET %s", url)
	}

	// Check status
	if resp.StatusCode != 200 {
		return "", errors.Errorf("Received non 200 status from %s", url)
	}

	// Check if revision sha is in the http Server header.
	if header := resp.Header.Get("Server"); header != "" {
		// TODO: use a regular expression
		t := strings.Split(header, "-")
		if len(t) > 1 {
			return t[len(t)-1], nil
		}
	}

	// Check if revision sha is in the body
	bodySha, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", errors.Errorf("Failed to parse body from %s", url)
	}

	return string(bodySha), nil
}

func checkDeployment(name, url, deployedSha string, check chan deployment) {
	log.Printf("Checking %s for newly deployed version\n", url)

	for {
		time.Sleep(awsecs.CheckIntervalSecs * time.Second)

		fetchedSha, err := fetchRevisionSha(url)
		if err != nil {
			log.Printf("Could not parse a gitsha version from header or body at %s\n", url)
			log.Printf("Error: %+v", err) // TODO: Remove if this is too noisy
			continue
		}

		log.Printf("Got %s from %s\n", fetchedSha, url)
		if len(fetchedSha) > 7 && strings.HasPrefix(deployedSha, fetchedSha) {
			dep := deployment{name: name}
			check <- dep
			return
		}
	}
}

func parseFlags() {
	flag.StringVar(&gitsha, "gitsha", "", "The gitsha of the version to be deployed")
	flag.StringVar(&configPath, "path", "", "The path to a gehen.yml config file")

	flag.Parse()

	// gitsha and path are required
	if gitsha == "" {
		fatal.Exit("Must provide a gitsha")
	} else if configPath == "" {
		fatal.Exit("Must provide the path to a gehen.yml file")
	}
}

func main() {
	err := sentry.Init(sentry.ClientOptions{Dsn: os.Getenv("SENTRY_DSN")})
	if err != nil {
		fatal.Exit("SENTRY_DSN is not set")
	}
	statsdClient, err := statsd.New(os.Getenv("DD_AGENT_HOST"))

	if err != nil {
		log.Fatal("Could not create StatsD agent (DD_AGENT_HOST may not be set)")
	}
	defer statsdClient.Flush()
	parseFlags()

	var services config.ServiceMap
	if configPath != "" {
		err = config.Init(configPath)
		if err != nil {
			fatal.ExitErr(err, "Failed reading config file.")
		}

		services = config.Config().Services
		if len(services) == 0 {
			fatal.Exit("gehen.yml must contain at least one service")
		}
	} else {
		fatal.Exit("Error: No config path set")
	}

	status := make(chan error)
	for name, s := range services {
		go func(serviceName, serviceCluster string) {
			status <- awsecs.Deploy(serviceName, serviceCluster, gitsha, statsdClient, services)
		}(name, s.Cluster)
	}

	for i := 0; i < len(services); i++ {
		err := <-status
		if err != nil {
			sentry.CaptureException(err)
			fatal.ExitErr(err, "Failed deploying to AWS.")
		}
	}

	check := make(chan deployment)
	for name, s := range services {
		go checkDeployment(name, s.URL, gitsha, check)
	}

	for finished := 0; finished < len(services); finished++ {
		select {
		case dep := <-check:
			if dep.err != nil {
				log.Printf("Version %s failed deployment to %s\n", gitsha, dep.name)
				os.Exit(1)
			}
			log.Printf("Traffic showing version %s on %s, waiting for old tasks to drain...\n", gitsha, dep.name)
		case <-time.After(timeoutMins * time.Minute):
			log.Println("Timed out while checking for deployed version of services")
			os.Exit(1)
		}
	}

	drained := make(chan string)
	errs := make(chan error)
	for name, s := range services {
		go awsecs.CheckDrain(name, s.Cluster, drained, errs, statsdClient, services)
	}

	for finished := 0; finished < len(services); finished++ {
		select {
		case name := <-drained:
			log.Printf("Version %s successfully deployed to %s\n", gitsha, name)
			doneEvent := &statsd.Event{
				// Title of the event.  Required.
				Title: "gehen.deploys.success",
				// Text is the description of the event.  Required.
				Text: "Gehen finished deploying " + name,
			}
			err := statsdClient.Event(doneEvent)
			if err != nil {
				sentry.CaptureException(err)
			}
		case err := <-errs:
			log.Printf("Version %s successfully deployed but statsd event didnt send\n", gitsha)
			sentry.CaptureException(err)
		case <-time.After(timeoutMins * time.Minute):
			log.Println("Timed out while waiting for service to drain (old tasks are still running, go check datadog logs")
			os.Exit(1)
		}
	}
	log.Printf("Finished deploying all services")
}
