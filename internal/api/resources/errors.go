package resources

import "fmt"

// NotFoundError is returned when a queried resource does not exist.
type NotFoundError struct {
	Resource string
	Query    string
}

// AmbiguousError means a name that looks like an ID matches two different objects:
// the one with that ID and a different one with that name.
type AmbiguousError struct {
	Resource string
	Query    string
	ByID     string
	ByName   string
}

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("%s %q is ambiguous: it is the ID of %s and also the name of %s; write id:%s or name:%s", e.Resource, e.Query, e.ByID, e.ByName, e.Query, e.Query)
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("%s %q not found", e.Resource, e.Query)
}
