// Package platform contains the portable boundary of the native Windows host.
package platform

import "image"

type Event struct {
	Kind        string
	OperationID string
	Region      image.Rectangle
	Err         error
}
type ViewImage struct{ ID, Path, Label string }
