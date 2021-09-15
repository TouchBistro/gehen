package awsecs

import (
	"context"
	"log"

	"github.com/TouchBistro/gehen/config"
	"github.com/TouchBistro/goutils/color"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	"github.com/pkg/errors"
)

type EBClient interface {
	ListTargetsByRule(ctx context.Context, params *eventbridge.ListTargetsByRuleInput, optFns ...func(*eventbridge.Options)) (*eventbridge.ListTargetsByRuleOutput, error)
	PutTargets(ctx context.Context, params *eventbridge.PutTargetsInput, optFns ...func(*eventbridge.Options)) (*eventbridge.PutTargetsOutput, error)
}

type UpdateScheduledTaskArgs struct {
	Task       *config.ScheduledTask
	IsRollback bool
	EBClient   EBClient
	ECSClient  ECSClient
}

func UpdateScheduledTask(ctx context.Context, args UpdateScheduledTaskArgs) error {
	task := args.Task
	// Retrieve targets for schedule task eventbridge rule
	// This will contain the ECS information like the task def
	respListTargets, err := args.EBClient.ListTargetsByRule(ctx, &eventbridge.ListTargetsByRuleInput{
		Rule: &task.Name,
	})
	if err != nil {
		return errors.Wrapf(err, "failed to find eventbridge rule for scheduled task %s", task.Name)
	}

	// There should only be 1 target if it was created in ECS. If it was created in
	// event bridge this might need to be modified to be more flexible.
	if len(respListTargets.Targets) != 1 {
		return errors.Wrapf(err, "expected 1 target for scheduled task rule, found %d", len(respListTargets.Targets))
	}

	awsTarget := respListTargets.Targets[0]

	var newTaskDefARN string
	if args.IsRollback {
		// If rollback just use the previous task def
		newTaskDefARN = task.PreviousTaskDefinitionARN
	} else {
		// Create a new revision of the task def using the new git sha
		taskDefARN := *awsTarget.EcsParameters.TaskDefinitionArn
		log.Printf("Found current task definition: %s\n", taskDefARN)

		// TODO(ohsabry): See if we want to support specifying containers for scheduled tasks
		// or if this is even allowed by ECS
		// Passing an empty array as the containers to update means gehen will update all
		// containers within this task definition to the new sha.
		updateTaskDefRes, err := updateTaskDef(ctx, taskDefARN, task.Gitsha, task.UpdateStrategy, []string{}, args.ECSClient)
		if err != nil {
			return errors.Wrapf(err, "failed to update task def for scheduled task: %s", task.Name)
		}

		log.Printf(
			"Registered new task definition %s, updating scheduled task %s\n",
			color.Cyan(updateTaskDefRes.newTaskDefARN),
			color.Cyan(task.Name),
		)
		newTaskDefARN = updateTaskDefRes.newTaskDefARN

		// Set dynamic task values
		task.PreviousGitsha = updateTaskDefRes.previousGitsha
		task.PreviousTaskDefinitionARN = taskDefARN
		task.TaskDefinitionARN = updateTaskDefRes.newTaskDefARN
	}

	// Just update the Task Def and use the same target
	// This way we can make sure all the other config is preserved
	awsTarget.EcsParameters.TaskDefinitionArn = &newTaskDefARN
	respPutTargets, err := args.EBClient.PutTargets(ctx, &eventbridge.PutTargetsInput{
		Rule:    &task.Name,
		Targets: []ebtypes.Target{awsTarget},
	})
	if err != nil {
		return errors.Wrapf(err, "failed to update targets for scheduled task rule %s", task.Name)
	}

	if respPutTargets.FailedEntryCount > 0 {
		for _, e := range respPutTargets.FailedEntries {
			log.Printf("Failed to update entry: %v\n", e)
		}

		return errors.Errorf("failed to update scheduled task entries: %s", task.Name)
	}

	return nil
}
