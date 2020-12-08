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
	"github.com/aws/aws-sdk-go/service/eventbridge/eventbridgeiface"
	"github.com/pkg/errors"
)

var (
	// Deployment check timeout in minutes
	timeoutDuration = 5 * time.Minute
	// Check interval in seconds
	checkIntervalDuration = 15 * time.Second
)

// ErrTimedOut represents the fact that a timeout occurred while waiting
// for a service to deploy or drain.
var ErrTimedOut = errors.New("deploy: timed out while checking for event")

// ErrNoDeployCheckURL is returned by CheckDeployed if the service has no URL set.
var ErrNoDeployCheckURL = errors.New("deploy: service has no URL to check deployment")

// Result represents the result of a deploy action.
// If the action failed err will be non-nil.
type Result struct {
	Service *config.Service
	Err     error
}

// TimeoutDuration sets the duration to wait for CheckDeployed and CheckDrained
// before timing out.
// Default is 5 minutes.
func TimeoutDuration(d time.Duration) {
	timeoutDuration = d
}

// CheckIntervalDuration sets the duration of how frequently to check
// if a service has deployed or drained.
// Default is 15 seconds.
func CheckIntervalDuration(d time.Duration) {
	checkIntervalDuration = d
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
	resultChan := make(chan Result)

	for _, s := range services {
		go func(service *config.Service) {
			// If service has no URL set, skip deploy check
			if service.URL == "" {
				resultChan <- Result{service, ErrNoDeployCheckURL}
				return
			}

			log.Printf("Checking %s for newly deployed version of %s\n", color.Blue(service.URL), color.Cyan(service.Name))

			for {
				time.Sleep(checkIntervalDuration)

				fetchedSha, err := fetchRevisionSha(service.URL)
				if err != nil {
					log.Printf("Could not parse a Git SHA version from header or body at %s\n", color.Blue(service.URL))
					log.Printf("Error: %v", err)
					continue
				}

				log.Printf("Got %s from %s\n", color.Magenta(fetchedSha), color.Blue(service.URL))
				if len(fetchedSha) > 7 && strings.HasPrefix(service.Gitsha, fetchedSha) {
					resultChan <- Result{Service: service}
					return
				}
			}
		}(s)
	}

	// Set of service names that completed the deploy check
	completedServices := make(map[string]bool)
	var results []Result

loop:
	for i := 0; i < len(services); i++ {
		select {
		case result := <-resultChan:
			if result.Err != nil {
				log.Printf(
					"Traffic showing version %s on %s, waiting for old versions to stop...\n",
					color.Green(result.Service.Gitsha),
					color.Cyan(result.Service.Name),
				)
			}
			completedServices[result.Service.Name] = true
			results = append(results, result)
		case <-time.After(timeoutDuration):
			// Stop looping, anything that didn't succeed has now failed
			break loop
		}
	}

	// Figure out which, if any, services timed out
	for _, s := range services {
		completed := completedServices[s.Name]
		if !completed {
			results = append(results, Result{s, ErrTimedOut})
		}
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
				time.Sleep(checkIntervalDuration)
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
		case <-time.After(timeoutDuration):
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

// ScheduledTaskResult represents the result of a scheduled task action.
// If the action failed err will be non-nil.
type ScheduledTaskResult struct {
	Task *config.ScheduledTask
	Err  error
}

// UpdateScheduledTasks will update the ECS scheduled tasks to use the new version of the service.
func UpdateScheduledTasks(tasks []*config.ScheduledTask, ebClient eventbridgeiface.EventBridgeAPI, ecsClient ecsiface.ECSAPI) []ScheduledTaskResult {
	resultChan := make(chan ScheduledTaskResult)

	// Update all the tasks concurrently
	for _, t := range tasks {
		go func(task *config.ScheduledTask) {
			err := awsecs.UpdateScheduledTask(awsecs.UpdateScheduledTaskArgs{
				Task:      task,
				EBClient:  ebClient,
				ECSClient: ecsClient,
			})
			resultChan <- ScheduledTaskResult{task, err}
		}(t)
	}

	// Collect and report the results
	results := make([]ScheduledTaskResult, len(tasks))
	for i := 0; i < len(tasks); i++ {
		results[i] = <-resultChan
	}

	return results
}

// RollbackScheduledTasks will change the ECS scheduled tasks to use the previous version of the service.
func RollbackScheduledTasks(tasks []*config.ScheduledTask, ebClient eventbridgeiface.EventBridgeAPI, ecsClient ecsiface.ECSAPI) []ScheduledTaskResult {
	resultChan := make(chan ScheduledTaskResult)

	// Rollback all the task concurrently
	for _, t := range tasks {
		go func(task *config.ScheduledTask) {
			err := awsecs.UpdateScheduledTask(awsecs.UpdateScheduledTaskArgs{
				Task: task,
				// The func will handle using the correct task def ARN, no need to swap ourselves
				IsRollback: true,
				EBClient:   ebClient,
				ECSClient:  ecsClient,
			})
			resultChan <- ScheduledTaskResult{task, err}
		}(t)
	}

	results := make([]ScheduledTaskResult, len(tasks))
	for i := 0; i < len(tasks); i++ {
		results[i] = <-resultChan
	}

	return results
}

func fetchRevisionSha(url string) (string, error) {
	resp, err := http.Get(url)
	if resp != nil {
		defer resp.Body.Close()
	}

	if err != nil {
		return "", errors.Wrapf(err, "Failed to HTTP GET %s", url)
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
