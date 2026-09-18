package kernel

// Level is the level of a check or a finding (SDD §4).
type Level string

const (
	Must   Level = "MUST"
	Should Level = "SHOULD"
	Info   Level = "INFO"
)
