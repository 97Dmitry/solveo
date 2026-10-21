package llm

import "context"

// Account describes the sign-in state of an adapter-owned connection. It never
// carries credentials.
type Account struct {
	SignedIn bool   `json:"signed_in"`
	Kind     string `json:"kind"`
}

// LoginPrompt is a pending device-code sign-in the user completes in a browser.
type LoginPrompt struct {
	ID   string `json:"id"`
	URL  string `json:"url"`
	Code string `json:"code"`
}

// Connection is optional: an adapter with its own persistent connection and
// account. Prepare may start the connection in the background but must never
// start a model turn.
type Connection interface {
	Account(context.Context) (Account, error)
	// StartLogin returns the prompt; done reports completion, failure or
	// cancellation once, possibly before StartLogin itself returns.
	StartLogin(ctx context.Context, done func(error)) (LoginPrompt, error)
	CancelLogin(ctx context.Context, id string) error
	Prepare(context.Context) error
	Close(context.Context) error
}
