package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/DataDog/datadog-go/statsd"
	"github.com/TouchBistro/gehen/config"
	"github.com/TouchBistro/gehen/deploy"
	"github.com/TouchBistro/goutils/color"
	"github.com/TouchBistro/goutils/fatal"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials/stscreds"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/aws/aws-sdk-go/service/eventbridge"
	"github.com/getsentry/sentry-go"
	"github.com/pkg/errors"
)

// Set by goreleaser at build time
var version string

// Flag values
var (
	versionFlag bool
	gitsha      string
	configPath  string
)

var (
	useSentry    = false
	statsdClient *statsd.Client
)

func sendStatsdEvents(services []*config.Service, eventTitle, eventText string) {
	if statsdClient == nil {
		return
	}

	for _, s := range services {
		event := &statsd.Event{
			// Title of the event.  Required.
			Title: eventTitle,
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

func cleanup() {
	if statsdClient != nil {
		// Increment metric to test that this stuff is working properly
		err := statsdClient.Incr("gehen.debug.completed", nil, 1)
		if err != nil {
			err = errors.Wrap(err, "failed to increment metric")
			if useSentry {
				sentry.CaptureException(err)
			}
		}

		statsdClient.Flush()
	}
}

func performRollback(services []*config.Service, scheduledTasks []*config.ScheduledTask, ebClient *eventbridge.EventBridge, ecsClient *ecs.ECS) {
	rollbackResults := deploy.Rollback(services, ecsClient)
	rollbackFailed := false

	for _, result := range rollbackResults {
		if result.Err == nil {
			continue
		}

		rollbackFailed = true
		log.Printf("Failed to create rollback to %s for %s", color.Magenta(result.Service.Gitsha), color.Cyan(result.Service.Name))
		log.Printf("Error: %v", result.Err)

		if useSentry {
			sentry.CaptureException(result.Err)
		}
	}

	if rollbackFailed {
		fatal.Exit(color.Red("🚨 Failed to create rollbacks for services 🚨"))
	}

	sendStatsdEvents(services, "gehen.rollbacks.started", "Gehen started a rollback for service %s")

	checkDeployedResults := deploy.CheckDeployed(services)
	checkDeployedFailed := false

	for _, result := range checkDeployedResults {
		if result.Err == nil {
			continue
		}

		checkDeployedFailed = true

		if errors.Is(result.Err, deploy.ErrTimedOut) {
			log.Printf(
				"Timed out while checking for rolled back version %s of %s",
				color.Magenta(result.Service.Gitsha),
				color.Cyan(result.Service.Name),
			)
			continue
		}

		log.Printf(
			"Failed to check for rolled back version %s of %s",
			color.Magenta(result.Service.Gitsha),
			color.Cyan(result.Service.Name),
		)
		log.Printf("Error: %v", result.Err)

		if useSentry {
			sentry.CaptureException(result.Err)
		}
	}

	if checkDeployedFailed {
		log.Println("This means your service failed to boot, or was unable to serve requests.")
		log.Println("Your next step should be to check the logs for your service to find out why.")
		fatal.Exit(color.Red("🚨 Failed to confirm services rolled back 🚨"))
	}

	sendStatsdEvents(services, "gehen.rollbacks.draining", "Gehen is checking for service rollback drain on %s")

	checkDrainedResults := deploy.CheckDrained(services, ecsClient)
	checkDrainedFailed := false

	for _, result := range checkDrainedResults {
		if result.Err == nil {
			continue
		}

		checkDrainedFailed = true

		if errors.Is(result.Err, deploy.ErrTimedOut) {
			log.Printf("Timed out while waiting for new versions of %s to stop running", color.Cyan(result.Service.Name))
			continue
		}

		log.Printf("Failed to check if new deployments of %s stopped", color.Cyan(result.Service.Name))
		log.Printf("Error: %v", result.Err)

		if useSentry {
			sentry.CaptureException(result.Err)
		}
	}

	if checkDrainedFailed {
		log.Println(color.Yellow("The rollback was successful but some of the newer versions are still running"))
		log.Println(color.Yellow("Please investigate why this is the case"))
	} else {
		sendStatsdEvents(services, "gehen.rollbacks.completed", "Gehen successfully rolled back %s")
	}

	// Need to rollback scheduled tasks though since they will likely fail as well
	// Also they would have inconsitent versions
	rollbackScheduledTaskResults := deploy.RollbackScheduledTasks(scheduledTasks, ebClient, ecsClient)
	rollbackScheduledTasksFailed := false

	for _, result := range rollbackScheduledTaskResults {
		if result.Err == nil {
			continue
		}

		rollbackScheduledTasksFailed = true
		log.Printf(
			"Failed to roll back scheduled task %s to version %s",
			color.Cyan(result.Task.Name),
			color.Magenta(result.Task.PreviousGitsha),
		)
		log.Printf("Error: %v", result.Err)

		if useSentry {
			sentry.CaptureException(result.Err)
		}
	}

	if rollbackScheduledTasksFailed {
		fatal.Exit(color.Red("Failed to roll back some scheduled tasks"))
	}

	fatal.Exit(color.Yellow("🚨 Finished rolling back services 🚨"))
}

func main() {
	// Handle flags
	flag.BoolVar(&versionFlag, "version", false, "Prints the current gehen version")
	flag.StringVar(&gitsha, "gitsha", "", "The gitsha of the version to be deployed")
	flag.StringVar(&configPath, "path", "gehen.yml", "The path to a gehen.yml config file")

	flag.Parse()

	if versionFlag {
		if version == "" {
			version = "source"
		}

		fmt.Printf("gehen version %s\n", version)
		os.Exit(0)
	}

	// gitsha is required
	if gitsha == "" {
		fatal.Exit("Must provide a gitsha")
	}

	// Initialize observability libraries
	// Sentry for error tracking, Datadog StatsD for metrics

	if sentryDSN, ok := os.LookupEnv("SENTRY_DSN"); ok {
		err := sentry.Init(sentry.ClientOptions{Dsn: sentryDSN})
		if err != nil {
			fatal.ExitErr(err, "Failed to initialize Sentry SDK.")
		}
		useSentry = true
	}

	if ddAgentHost, ok := os.LookupEnv("DD_AGENT_HOST"); ok {
		client, err := statsd.New(ddAgentHost, statsd.Option(func(o *statsd.Options) error {
			// Try creating an unbuffered client to see if completed events show up
			o.MaxMessagesPerPayload = 1
			return nil
		}))
		if err != nil {
			fatal.ExitErr(err, "Could not create StatsD agent (DD_AGENT_HOST may not be set)")
		}

		statsdClient = client
	}

	defer cleanup()

	// defers are skipped if Exit is used so we need to make sure flush still gets called
	fatal.OnExit(cleanup)

	// gehen config, get and validate services

	parsedConfig, err := config.Read(configPath, gitsha)
	if err != nil {
		fatal.ExitErr(err, "Failed to get services from config file")
	}

	if len(parsedConfig.Services) == 0 && len(parsedConfig.ScheduledTasks) == 0 {
		fatal.Exit("gehen.yml must contain at least one service or scheduled task")
	}

	sess := session.Must(session.NewSession(&aws.Config{
		Region: aws.String("us-east-1"),
	}))

	// Connect to ECS and EventBridge APIs
	var ecsClient *ecs.ECS
	var ebClient *eventbridge.EventBridge

	if parsedConfig.Role != nil {
		awsConfig := aws.NewConfig().WithCredentials(stscreds.NewCredentials(sess, parsedConfig.Role.ARN))

		ecsClient = ecs.New(sess, awsConfig)
		ebClient = eventbridge.New(sess, awsConfig)
	} else {
		ecsClient = ecs.New(sess)
		ebClient = eventbridge.New(sess)
	}

	if parsedConfig.TimeoutMinutes != 0 {
		deploy.TimeoutDuration(time.Duration(parsedConfig.TimeoutMinutes) * time.Minute)
	}

	// DEPLOYMENT ZONE //

	// Update scheduled tasks first so if this fails we don't need to worry about rolling back services
	updateScheduledTaskResults := deploy.UpdateScheduledTasks(parsedConfig.ScheduledTasks, ebClient, ecsClient)
	updateScheduledTasksFailed := false

	for _, result := range updateScheduledTaskResults {
		if result.Err == nil {
			continue
		}

		updateScheduledTasksFailed = true
		log.Printf(
			"Failed to update scheduled task %s to version %s",
			color.Cyan(result.Task.Name),
			color.Magenta(result.Task.Gitsha),
		)
		log.Printf("Error: %v", result.Err)

		if useSentry {
			sentry.CaptureException(result.Err)
		}
	}

	if updateScheduledTasksFailed {
		fatal.Exit(color.Red("Failed to update some scheduled tasks"))
	}

	deployEnabled := parsedConfig.UpdateStrategy != config.UpdateStrategyNone
	if deployEnabled {
		deployResults := deploy.Deploy(parsedConfig.Services, ecsClient)
		deployFailed := false
		succeededServices := make([]*config.Service, 0)

		for _, result := range deployResults {
			if result.Err == nil {
				succeededServices = append(succeededServices, result.Service)
				continue
			}

			deployFailed = true
			log.Printf(
				"Failed to create new deployment to version %s for %s",
				color.Magenta(result.Service.Gitsha),
				color.Cyan(result.Service.Name),
			)
			log.Printf("Error: %v", result.Err)

			if useSentry {
				sentry.CaptureException(result.Err)
			}
		}

		if deployFailed {
			// If deploying failed we need to rollback all services that succeeded so that they aren't in inconsitent states
			// If deploy failed that means the new version wasn't even registered on ECS so we only need to rollback ones that succeeded
			log.Println(color.Red("Failed to create new versions of some services"))
			log.Println(color.Yellow("Rolling back services that succeeded to prevent inconsistent states"))
			performRollback(succeededServices, parsedConfig.ScheduledTasks, ebClient, ecsClient)
		}

		sendStatsdEvents(parsedConfig.Services, "gehen.deploys.started", "Gehen started a deploy for service %s")
	}

	checkDeployedResults := deploy.CheckDeployed(parsedConfig.Services)
	checkDeployedFailed := false

	for _, result := range checkDeployedResults {
		if result.Err == nil || result.Err == deploy.ErrNoDeployCheckURL {
			continue
		}

		checkDeployedFailed = true

		if errors.Is(result.Err, deploy.ErrTimedOut) {
			log.Printf(
				"Timed out while checking for deployed version %s of %s",
				color.Magenta(result.Service.Gitsha),
				color.Cyan(result.Service.Name),
			)
			continue
		}

		log.Printf(
			"Failed to check for deployed version %s of %s",
			color.Magenta(result.Service.Gitsha),
			color.Cyan(result.Service.Name),
		)
		log.Printf("Error: %v", result.Err)

		if useSentry {
			sentry.CaptureException(result.Err)
		}
	}

	if checkDeployedFailed {
		// If check deployment failed we need to roll everything back
		// Services that timed out are likely stuck in a death loop
		log.Println(color.Red("Some services failed deployment"))
		log.Println("This means your service failed to boot, or was unable to serve requests.")
		log.Println("Your next step should be to check the logs for your service to find out why.")

		if !deployEnabled {
			fatal.Exit("❌ Deployment failed")
		}

		log.Println(color.Yellow("Rolling all services back to the previous version"))
		performRollback(parsedConfig.Services, parsedConfig.ScheduledTasks, ebClient, ecsClient)
	}

	sendStatsdEvents(parsedConfig.Services, "gehen.deploys.draining", "Gehen is checking for service drain on %s")

	checkDrainedResults := deploy.CheckDrained(parsedConfig.Services, ecsClient)
	checkDrainedFailed := false

	for _, result := range checkDrainedResults {
		if result.Err == nil {
			continue
		}

		checkDrainedFailed = true

		if errors.Is(result.Err, deploy.ErrTimedOut) {
			log.Printf("Timed out while waiting for old versions of %s to stop running", color.Cyan(result.Service.Name))
			continue
		}

		log.Printf("Failed to check if old version of %s are gone", color.Cyan(result.Service.Name))
		log.Printf("Error: %v", result.Err)

		if useSentry {
			sentry.CaptureException(result.Err)
		}
	}

	if checkDrainedFailed {
		log.Println(color.Yellow("Some services still have the old version running"))
		log.Println(color.Yellow("This means there are two different versions of the same service in production"))
		log.Println(color.Yellow("Please investigate why this is the case"))
	} else {
		sendStatsdEvents(parsedConfig.Services, "gehen.deploys.completed", "Gehen successfully deployed %s")
	}

	log.Println(color.Green("🚀 Finished deploying all services 🚀"))
}
