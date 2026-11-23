package application

import (
	"context"
	"time"

	"liberty.local/liberty/internal/domain"
)

// PetWorkflow is the narrow reward port used after a business operation has
// committed. Reward failures never undo an accepted job or completed summary.
// The pet repository owns persistent event-key deduplication.
type PetWorkflow interface {
	Workflow(context.Context, string, string) (domain.PetProfile, error)
}

func optionalPetWorkflow(hooks []PetWorkflow) PetWorkflow {
	if len(hooks) > 0 {
		return hooks[0]
	}
	return nil
}

func recordPetWorkflow(ctx context.Context, pet PetWorkflow, event, key string) {
	if pet != nil {
		accounting, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_, _ = pet.Workflow(accounting, event, key)
	}
}
