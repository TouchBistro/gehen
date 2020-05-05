package main

import (
	"flag"
	"log"
	"os"
	"time"

	"github.com/TouchBistro/gehen/awsecs"
	"github.com/TouchBistro/gehen/check"
	"github.com/TouchBistro/gehen/config"
	"github.com/TouchBistro/goutils/fatal"
	"github.com/getsentry/sentry-go"
)

const (
	timeoutMins       = 10 // deployment check timeout in minutes
	checkIntervalSecs = 15 // check interval in seconds
)

var (
	gitsha       string
	migrationCmd string
	configPath   string
)

type deployment struct {
	name string
	err  error
}

func checkDeployment(url, testURL, expectedSha string) error {
	log.Printf("Checking %s for newly deployed version\n", url)

	for {
		time.Sleep(checkIntervalSecs * time.Second)

		ok, err := check.Deploy(url, expectedSha)
		if err != nil {
			log.Printf("%+v", err) // TODO: Remove if this is too noisy
			continue
		}

		// Not deployed yet, keep checking
		if !ok {
			continue
		}

		// Successfully deployed, run smoke test if it exists
		if testURL != "" {
			log.Printf("Checking %s for smoke-test test suite\n", testURL)
			err := check.SmokeTest(testURL)
			if err != nil {
				log.Printf("Help! I've fallen and I can't get up!: %+v", err) // TODO: Remove if this is too noisy
				return err
			}
		}

		return nil
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
	err = config.Init(configPath)
	if err != nil {
		fatal.ExitErr(err, "Failed reading config file.")
	}

	services = config.Config().Services
	if len(services) == 0 {
		fatal.Exit("gehen.yml must contain at least one service")
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
		go func(name, gitsha string, service config.Service) {
			err := checkDeployment(service.URL, service.TestURL, gitsha)
			check <- deployment{
				name: name,
				err:  err,
			}
		}(name, gitsha, s)
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
