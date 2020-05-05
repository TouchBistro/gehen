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
	"github.com/TouchBistro/goutils/fatal"
	"github.com/getsentry/sentry-go"
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

func checkLifeAlert(url string) error {
	client := &http.Client{}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return errors.Errorf("Failed to build HTTP request for %s", url)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", os.Getenv("CHECKER_BEARER_TOKEN")))
	resp, err := client.Do(req)
	if resp != nil {
		defer resp.Body.Close()
	}

	if err != nil {
		return errors.Errorf("Failed to HTTP GET %s", url)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return errors.Errorf("Could not parse body from %s", url)
	}

	if resp.StatusCode != 200 {
		return errors.Errorf("Error HTTP Status %d returned from Life Alert check with error %s", resp.StatusCode, string(body))
	}

	return nil
}

func checkDeployment(name, url, testUrl, deployedSha string, check chan deployment) {
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

			if testUrl != "" {
				log.Printf("Checking %s for life-alert test suite\n", testUrl)
				err := checkLifeAlert(testUrl)
				if err != nil {
					log.Printf("Help! I've fallen and I can't get up!: %+v", err) // TODO: Remove if this is too noisy
					dep.err = err
				}
			}

			check <- dep
			return
		}
	}
}

func parseFlags() {
	flag.StringVar(&gitsha, "gitsha", "", "The gitsha of the version to be deployed")
	flag.StringVar(&migrationCmd, "migrate", "", "Launch a one-off migration task along with the service update")
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
			sentry.CaptureException(err)
			fatal.ExitErr(err, "Failed deploying to AWS.")
		}
	}

	check := make(chan deployment)
	for name, s := range services {
		go checkDeployment(name, s.URL, s.TestURL, gitsha, check)
	}

	for finished := 0; finished < len(services); finished++ {
		select {
		case dep := <-check:
			if dep.err != nil {
				log.Printf("Version %s failed deployment to %s\n", gitsha, dep.name)
				os.Exit(1)
			}
			log.Printf("Version %s successfully deployed to %s\n", gitsha, dep.name)
		case <-time.After(timeoutMins * time.Minute):
			log.Println("Timed out while checking for deployed version of services")
			os.Exit(1)
		}
	}
}
