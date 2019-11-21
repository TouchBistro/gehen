package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/TouchBistro/gehen/awsecs"
	"github.com/TouchBistro/gehen/config"
	"github.com/getsentry/raven-go"
	"github.com/pkg/errors"
)

const (
	timeoutMins       = 10 // deployment check timeout in minutes
	checkIntervalSecs = 15 // check interval in seconds
)

var (
	cluster      string
	service      string
	gitsha       string
	migrationCmd string
	versionURL   string
	configPath   string
)

type deployment struct {
	name string
	err  error
}

func fetchRevisionSha(url string) (string, error) {
	resp, err := http.Get(url)
	if resp != nil {
		defer resp.Body.Close()
	}

	if err != nil {
		return "", errors.New(fmt.Sprintf("Failed to HTTP GET %s", url))
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
		return "", errors.New(fmt.Sprintf("Failed to parse body from %s", url))
	}

	return string(bodySha), nil
}

func checkLifeAlert(url string) error {
	resp, err := http.Get(url)
	if resp != nil {
		defer resp.Body.Close()
	}

	if err != nil {
		return errors.New(fmt.Sprintf("Failed to HTTP GET %s", url))
	}

	if resp.StatusCode != 200 {
		return errors.New(fmt.Sprintf("Error HTTP Status %d returned from Life Alert check", resp.StatusCode))
	}

	return nil
}

func checkDeployment(name, url string, testUrl *string, deployedSha string, check chan deployment) {
	log.Printf("Checking %s for newly deployed version\n", url)

	for {
		time.Sleep(checkIntervalSecs * time.Second)

		fetchedSha, err := fetchRevisionSha(url)
		if err != nil {
			log.Printf("Could not parse a gitsha version from header or body at %s\n", url)
			log.Printf("Error: %+v", err) // TODO: Remove if this is too noisy
			continue
		}

		log.Printf("Got %s from %s\n", fetchedSha, url)
		if len(fetchedSha) > 7 && strings.HasPrefix(deployedSha, fetchedSha) {
			dep := deployment{name: name}

			if testUrl != nil {
				log.Printf("Checking %s for life-alert test suite\n", *testUrl)
				err := checkLifeAlert(*testUrl)
				if err != nil {
					log.Printf("Help! I've fallen and I can't get up!: %+v", err) // TODO: Remove if this is too noisy
					dep.err = err
					continue
				}
			}

			check <- dep
			return
		}
	}
}

func parseFlags() {
	flag.StringVar(&cluster, "cluster", "", "The full cluster ARN to deploy this service to")
	flag.StringVar(&service, "service", "", "The service name running this service on ECS")
	flag.StringVar(&gitsha, "gitsha", "", "The gitsha of the version to be deployed")
	flag.StringVar(&migrationCmd, "migrate", "", "Launch a one-off migration task along with the service update")
	flag.StringVar(&versionURL, "url", "", "The URL to check for the deployed version")
	flag.StringVar(&configPath, "path", "", "The path to a gehen.yml config file")

	flag.Parse()

	// gitsha is always required, then require either path or cluster and service and versionURL
	if gitsha == "" {
		log.Fatalln("Must provide gitsha")
	} else if configPath == "" && (cluster == "" || service == "" || versionURL == "") {
		log.Fatalln("Must provide cluster, service, and versionURL")
	} else if configPath != "" && (cluster != "" || service != "" || versionURL != "") {
		log.Fatalln("Must specify either configPath or all of cluster, service, and versionURL")
	}
}

func main() {
	err := raven.SetDSN(os.Getenv("SENTRY_DSN"))
	if err != nil {
		log.Fatal("SENTRY_DSN is not set")
	}
	parseFlags()

	var services config.ServiceMap
	if configPath != "" {
		err = config.Init(configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed reading config file. Error: %+v\n", err)
			os.Exit(1)
		}

		services = config.Config().Services
		if len(services) == 0 {
			fmt.Fprintln(os.Stderr, "gehen.yml must contain at least one service")
			os.Exit(1)
		}
	} else {
		services = config.ServiceMap{
			service: {
				Cluster: cluster,
				URL:     versionURL,
			},
		}
	}

	status := make(chan error)
	for name, s := range services {
		go func(serviceName, serviceCluster string) {
			status <- awsecs.Deploy(migrationCmd, serviceName, serviceCluster, gitsha)
		}(name, s.Cluster)
	}

	for i := 0; i < len(services); i++ {
		err := <-status
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed deploying to aws. Error: %+v\n", err)
			raven.CaptureErrorAndWait(err, nil)
			os.Exit(1)
		}
	}

	check := make(chan deployment)
	for name, s := range services {
		go checkDeployment(name, s.URL, s.TestURL, gitsha, check)
	}

	for finished := 0; finished < len(services); finished++ {
		select {
		case deployment := <-check:
			if deployment.err != nil {
				log.Printf("Version %s successfully deployed to %s\n", gitsha, deployment.name)
				os.Exit(1)
			}
			log.Printf("Version %s successfully deployed to %s\n", gitsha, deployment.name)
		case <-time.After(timeoutMins * time.Minute):
			log.Println("Timed out while checking for deployed version of services")
			os.Exit(1)
		}
	}
}
