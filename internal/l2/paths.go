package l2

// Path constants for L2 deployment artifacts and runtime directories.
// All paths are relative to the project root directory.
const (
	// localnetDirName is the root directory for all L2 runtime artifacts
	localnetDirName = ".localnet"

	// servicesDirName is the subdirectory for cloned repositories (publisher, op-rbuilder, sidecar, etc.)
	servicesDirName = "services"

	// stateDirName is the subdirectory for L1 deployment state
	stateDirName = "state"

	// networksDirName is the subdirectory for generated L2 network configurations
	networksDirName = "networks"
)
