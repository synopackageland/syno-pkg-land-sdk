//go:build !linux

package idgateway

import (
	"context"
	"errors"
)

func notifyOptionalGateway(string) error {
	return errors.New("optional passthrough reload requires Linux")
}
func (f *Front) watchOptional(context.Context) (func(), error) {
	if f.optional == nil {
		return func() {}, nil
	}
	return nil, errors.New("optional passthrough reload requires Linux")
}
