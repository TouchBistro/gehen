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
)

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

func checkDeployment(url string, deployedSha string, check chan bool) {
	log.Printf("Checking %s for newly deployed version\n", versionURL)

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
			check <- true
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

	flag.Parse()

	if cluster == "" || service == "" || gitsha == "" || versionURL == "" {
		log.Fatalln("Unset flags, need cluster, service, versionURL and gitsha")
	}
}

func main() {
	raven.SetDSN(os.Getenv("SENTRY_DSN"))
	parseFlags()

	err := awsecs.Deploy(migrationCmd, service, cluster, gitsha)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed deploying to aws. Error: %+v\n", err)
		raven.CaptureErrorAndWait(err, nil)
		os.Exit(1)
	}

	check := make(chan bool)
	go checkDeployment(versionURL, gitsha, check)

	select {
	case <-check:
		log.Printf("Version %s successfully deployed to %s\n", gitsha, service)
		return
	case <-time.After(timeoutMins * time.Minute):
		log.Printf("Timed out while checking for deployed version on %s\n", service)
		os.Exit(1)
	}
}
