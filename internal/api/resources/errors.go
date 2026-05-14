package resources

import "fmt"

// NotFoundError is returned when a queried resource does not exist.
type NotFoundError struct {
	Resource string
	Query    string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("%s %q not found", e.Resource, e.Query)
}
