package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/DataDog/datadog-go/statsd"
	"github.com/TouchBistro/gehen/config"
	"github.com/TouchBistro/gehen/deploy"
	"github.com/TouchBistro/goutils/fatal"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/getsentry/sentry-go"
	"github.com/pkg/errors"
)

// Flag values
var (
	gitsha     string
	configPath string
)

var (
	useSentry    = false
	statsdClient *statsd.Client
)

func sendStatsdEvents(services []config.Service, eventTitle, eventText string) {
	if statsdClient == nil {
		return
	}

	for _, s := range services {
		event := &statsd.Event{
			// Title of the event.  Required.
			Title: eventText,
			// Text is the description of the event.  Required.
			Text: fmt.Sprintf(eventText, s.Name),
			// Tags for the event.
			Tags: s.Tags,
		}

		err := statsdClient.Event(event)
		if err != nil {
			err = errors.Wrap(err, "cannot send statsd event")
			if useSentry {
				sentry.CaptureException(err)
			}
		}
	}
}

func main() {
	// Don't show stack traces because it's too noisy. Stack traces will be captured in Sentry
	fatal.ShowStackTraces = false

	// Handle flags
	flag.StringVar(&gitsha, "gitsha", "", "The gitsha of the version to be deployed")
	flag.StringVar(&configPath, "path", "", "The path to a gehen.yml config file")

	flag.Parse()

	// gitsha and path are required
	if gitsha == "" {
		fatal.Exit("Must provide a gitsha")
	} else if configPath == "" {
		fatal.Exit("Must provide the path to a gehen.yml file")
	}

	// Initialize observability libraries
	// Sentry for error tracking, Datadog StatsD for metrics

	if sentryDSN, ok := os.LookupEnv("SENTRY_DSN"); ok {
		err := sentry.Init(sentry.ClientOptions{Dsn: sentryDSN})
		if err != nil {
			fatal.ExitErr(err, "Failed to initialize Sentry SDK.")
		}
	}

	if ddAgentHost, ok := os.LookupEnv("DD_AGENT_HOST"); ok {
		client, err := statsd.New(ddAgentHost)
		if err != nil {
			fatal.ExitErr(err, "Could not create StatsD agent (DD_AGENT_HOST may not be set)")
		}

		statsdClient = client
		defer statsdClient.Flush()
	}

	// gehen config, get and validate services

	services, err := config.ReadServices(configPath, gitsha)
	if err != nil {
		fatal.ExitErr(err, "Failed to get services from config file")
	}

	if len(services) == 0 {
		fatal.Exit("gehen.yml must contain at least one service")
	}

	// Connect to ECS API
	sess := session.Must(session.NewSession(&aws.Config{
		Region: aws.String("us-east-1"),
	}))
	ecsClient := ecs.New(sess)

	// DEPLOYMENT ZONE //

	deployResults := deploy.Deploy(services, ecsClient)
	// TODO(@cszatmary) implement rollbacks
	// will do this in the next PR, for now keep the behaviour the same as before
	deployFailed := false

	for _, result := range deployResults {
		if result.Err == nil {
			continue
		}

		deployFailed = true
		log.Printf("Failed to deploy %s", result.Service.Name)
		log.Printf("Error: %v", result.Err)

		if useSentry {
			sentry.CaptureException(result.Err)
		}
	}

	if deployFailed {
		fatal.Exit("Failed deploying to AWS")
	}

	sendStatsdEvents(services, "gehen.deploys.started", "Gehen started a deploy for service %s")

	checkDeployedResults := deploy.CheckDeployed(services)
	checkDeployedFailed := false

	for _, result := range checkDeployedResults {
		if result.Err == nil {
			continue
		}

		checkDeployedFailed = true

		if errors.Is(result.Err, deploy.ErrTimedOut) {
			log.Printf("Timed out while checking for deployed version of %s", result.Service.Name)
			continue
		}

		log.Printf("Failed to check for deployed version of %s", result.Service.Name)
		log.Printf("Error: %v", result.Err)

		if useSentry {
			sentry.CaptureException(result.Err)
		}

	}

	if checkDeployedFailed {
		fatal.Exit("Failed to check for new versions of services")
	}

	sendStatsdEvents(services, "gehen.deploys.draining", "Gehen is checking for service drain on %s")

	checkDrainedResults := deploy.CheckDrained(services, ecsClient)
	checkDrainedFailed := false

	for _, result := range checkDrainedResults {
		if result.Err == nil {
			continue
		}

		checkDrainedFailed = true

		if errors.Is(result.Err, deploy.ErrTimedOut) {
			log.Printf("Timed out while waiting for %s to drain (old tasks are still running, go check datadog logs", result.Service.Name)
			continue
		}

		log.Printf("Failed to check if %s drained", result.Service.Name)
		log.Printf("Error: %v", result.Err)

		if useSentry {
			sentry.CaptureException(result.Err)
		}

	}

	if checkDrainedFailed {
		fatal.Exit("Failed to check if services have drained")
	}

	sendStatsdEvents(services, "gehen.deploys.completed", "Gehen successfully deployed %s")

	log.Printf("Finished deploying all services")
}
