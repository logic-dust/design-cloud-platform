package payload

// JobQuery is a scheduler read command (bjobs, bhosts, squeue, sinfo, sacct).
// The command line already carries every scheduler-specific argument.
type JobQuery struct {
	Command   *CommandSpec      `json:"command,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	EnvScript string            `json:"envScript,omitempty"`
}

// JobSubmit is a scheduler submit command (bsub, sbatch).
// Job name, queue, directories and the user command are arguments of Command.
type JobSubmit struct {
	Command   *CommandSpec      `json:"command,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	EnvScript string            `json:"envScript,omitempty"`
}

// JobControl is a scheduler control command (bkill, scancel, scontrol, ...).
type JobControl struct {
	Command   *CommandSpec      `json:"command,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	EnvScript string            `json:"envScript,omitempty"`
}

// JobModify is a scheduler modify command (bmod, scontrol update).
type JobModify struct {
	Command   *CommandSpec `json:"command,omitempty"`
	EnvScript string       `json:"envScript,omitempty"`
}
