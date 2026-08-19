package payload

// --- Scheduler Actions ---

// JobQuery for JOB_QUERY
type JobQuery struct {
	Command   *CommandSpec      `json:"command,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	EnvScript string            `json:"envScript,omitempty"`
	JobID     string            `json:"jobId"`
	User      string            `json:"user"`
	Queue     string            `json:"queue"`
}

// JobSubmit for JOB_SUBMIT
type JobSubmit struct {
	Command   *CommandSpec      `json:"command,omitempty"`
	Username  string            `json:"username"`
	JobName   string            `json:"jobName"`
	Queue     string            `json:"queue"`
	WorkDir   string            `json:"workDir"`
	LogDir    string            `json:"logDir"`
	Env       map[string]string `json:"env,omitempty"`
	EnvScript string            `json:"envScript,omitempty"`
}

// JobControl for JOB_CONTROL
type JobControl struct {
	Command     *CommandSpec      `json:"command,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	EnvScript   string            `json:"envScript,omitempty"`
	Operation   string            `json:"operation"`
	JobID       string            `json:"jobId"`
	TargetQueue string            `json:"targetQueue,omitempty"`
}

// JobModify for JOB_MODIFY
type JobModify struct {
	Command   *CommandSpec `json:"command,omitempty"`
	EnvScript string       `json:"envScript,omitempty"`
	JobID     string       `json:"jobId"`
	Queue     *string      `json:"queue,omitempty"`
	Name      *string      `json:"name,omitempty"`
}
