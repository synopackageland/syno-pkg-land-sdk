package main

// The immutable policy opts into administrator-selected scope. Only the
// password-confirmed root task may populate the protected runtime selection.
const installSelectedScope = "install-selected"
const maxScopeGeneration = uint64(1<<53 - 1)

type SelectedScope struct {
	Schema     int    `json:"schema"`
	Scope      string `json:"scope"`
	Generation uint64 `json:"generation"`
}
