//go:build !windows

package audio

import (
	"context"
	"errors"
)

type unsupported struct{}

// NewCapturer reports that capture needs Windows.
func NewCapturer() Capturer { return unsupported{} }

func (unsupported) Start(context.Context, Source, func(Frame), func(error)) (Device, func(), error) {
	return Device{}, nil, errors.New("захват звука доступен только в Windows")
}
