// Ported from packages/coding-agent/src/experimental/coordinator-entry.ts (pi v1.0.0).

package experimental

import (
	"errors"
	"os"
)

// RunCoordinatorEntrypoint validates the internal coordinator invocation role and starts the coordinator process.
func RunCoordinatorEntrypoint(args []string) error {
	role, ok, err := ConsumeInternalProcessRole()
	if err != nil {
		return err
	}
	if !ok || role != InternalRoleCoordinator {
		return errors.New("Coordinator entrypoint requires an internal coordinator invocation")
	}
	resolvedArgs, err := ConsumeInternalProcessArgs(args)
	if err != nil {
		return err
	}
	if len(resolvedArgs) == 0 && len(os.Args) > 1 {
		resolvedArgs = os.Args[1:]
	}
	return RunCoordinatorProcess(resolvedArgs)
}
