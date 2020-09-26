package deploy

import (
	"io/ioutil"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/TouchBistro/gehen/awsecs"
	"github.com/TouchBistro/gehen/config"
	"github.com/TouchBistro/goutils/color"
	"github.com/aws/aws-sdk-go/service/ecs/ecsiface"
	"github.com/pkg/errors"
)

// Deployment check timeout in minutes
const timeoutMins = 5

// Check interval in seconds
const checkIntervalSecs = 15

// ErrTimedOut represents the fact that a timeout occurred while waiting
// for a service to deploy or drain.
var ErrTimedOut = errors.New("deploy: timed out while checking for event")

// Result represents the result of a deploy action.
// If the action failed err will be non-nil.
type Result struct {
	Service *config.Service
	Err     error
}

// Deploy will deploy the given services to AWS ECS.
func Deploy(services []*config.Service, ecsClient ecsiface.ECSAPI) []Result {
	resultChan := make(chan Result)

	// Deploy all the services concurrently
	for _, s := range services {
		go func(service *config.Service) {
			err := awsecs.Deploy(service, ecsClient)
			resultChan <- Result{service, err}
		}(s)
	}

	// Collect and report the results
	results := make([]Result, len(services))
	for i := 0; i < len(services); i++ {
		results[i] = <-resultChan
	}

	return results
}

func Rollback(services []*config.Service, ecsClient ecsiface.ECSAPI) []Result {
	resultChan := make(chan Result)

	// Rollback all the services concurrently
	for _, s := range services {
		// Swap Gitshas and TaskDef ARNs to rollback
		gitsha := s.PreviousGitsha
		s.PreviousGitsha = s.Gitsha
		s.Gitsha = gitsha

		taskDefARN := s.PreviousTaskDefinitionARN
		s.PreviousTaskDefinitionARN = s.TaskDefinitionARN
		s.TaskDefinitionARN = taskDefARN

		go func(service *config.Service) {
			err := awsecs.UpdateService(service, ecsClient)
			resultChan <- Result{service, err}
		}(s)
	}

	results := make([]Result, len(services))
	for i := 0; i < len(services); i++ {
		results[i] = <-resultChan
	}

	return results
}

// CheckDeployed keeps pinging the services until it sees the new version has been deployed
// or it times out. If a service timed out Result.err will be ErrTimedOut.
func CheckDeployed(services []*config.Service) []Result {
	successChan := make(chan *config.Service)

	for _, s := range services {
		go func(service *config.Service) {
			log.Printf("Checking %s for newly deployed version of %s\n", color.Blue(service.URL), color.Cyan(service.Name))

			for {
				time.Sleep(checkIntervalSecs * time.Second)

				fetchedSha, err := fetchRevisionSha(service.URL)
				if err != nil {
					log.Printf("Could not parse a Git SHA version from header or body at %s\n", color.Blue(service.URL))
					log.Printf("Error: %v", err)
					continue
				}

				log.Printf("Got %s from %s\n", color.Magenta(fetchedSha), color.Blue(service.URL))
				if len(fetchedSha) > 7 && strings.HasPrefix(service.Gitsha, fetchedSha) {
					successChan <- service
					return
				}
			}
		}(s)
	}

	// Set of service names that succeeded
	succeededServices := make(map[string]bool)

loop:
	for i := 0; i < len(services); i++ {
		select {
		case service := <-successChan:
			log.Printf("Traffic showing version %s on %s, waiting for old versions to stop...\n", color.Green(service.Gitsha), color.Cyan(service.Name))
			succeededServices[service.Name] = true
		case <-time.After(timeoutMins * time.Minute):
			// Stop looping, anything that didn't succeed has now failed
			break loop
		}
	}

	results := make([]Result, len(services))
	for i, s := range services {
		result := Result{Service: s}

		succeeded := succeededServices[s.Name]
		if !succeeded {
			result.Err = ErrTimedOut
		}

		results[i] = result
	}

	return results
}

// CheckDrained keeps checking the services until it sees all old versions are gone
// or it times out. If a service timed out Result.err will be ErrTimedOut.
func CheckDrained(services []*config.Service, ecsClient ecsiface.ECSAPI) []Result {
	resultChan := make(chan Result)

	for _, s := range services {
		go func(service *config.Service) {
			for {
				time.Sleep(checkIntervalSecs * time.Second)
				log.Printf("Checking if old versions are gone for: %s\n", color.Cyan(service.Name))

				drained, err := awsecs.CheckDrain(service, ecsClient)
				if err != nil {
					// This should never error otherwise Deploy would have failed
					// If this happens abort because it will never succeed
					resultChan <- Result{service, err}
					return
				}

				if !drained {
					continue
				}

				resultChan <- Result{Service: service}
				return
			}
		}(s)
	}

	// Set of service names that succeeded
	succeededServices := make(map[string]bool)
	results := make([]Result, 0, len(services))

loop:
	for i := 0; i < len(services); i++ {
		select {
		case result := <-resultChan:
			log.Printf("Version %s successfully deployed to %s\n", color.Green(result.Service.Gitsha), color.Cyan(result.Service.Name))
			succeededServices[result.Service.Name] = true
			results = append(results, result)
		case <-time.After(timeoutMins * time.Minute):
			// Stop looping, anything that didn't succeed has now failed
			break loop
		}
	}

	// Figure out which, if any, services timed out
	for _, s := range services {
		succeeded := succeededServices[s.Name]
		if !succeeded {
			result := Result{s, ErrTimedOut}
			results = append(results, result)
		}
	}

	return results
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
