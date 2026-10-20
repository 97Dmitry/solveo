package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/97Dmitry/solveo/internal/llm"
)

type catalogProvider struct {
	*fakeProvider
	started chan struct{}
}

func (p *catalogProvider) Models(ctx context.Context) ([]llm.Model, error) {
	close(p.started)
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestModelDiscoveryIsIndependentAndShutdownCancelsIt(t *testing.T) {
	c, p, _ := setup(t)
	catalog := &catalogProvider{fakeProvider: p, started: make(chan struct{})}
	c.deps.Provider = catalog
	before := c.Snapshot()
	result := make(chan error, 1)
	go func() { _, err := c.ListModels(); result <- err }()
	<-catalog.started
	after := c.Snapshot()
	if after.Revision != before.Revision || after.ActiveChatID != before.ActiveChatID || after.Operation.Phase != "idle" || after.Config != before.Config {
		t.Fatal("discovery changed chat state")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("catalog did not cancel: %v", err)
	}
	if _, err := c.ListModels(); err == nil {
		t.Fatal("discovery allowed after shutdown")
	}
}
