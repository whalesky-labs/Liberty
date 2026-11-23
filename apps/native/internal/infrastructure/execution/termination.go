package execution

import "liberty.local/liberty/internal/infrastructure/runner"

type Processes struct{}

func (Processes) Terminate(pid *uint32, identity *string) error {
	return runner.TerminatePersisted(pid, identity)
}
