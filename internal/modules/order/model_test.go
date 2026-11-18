package order

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCanTransition(t *testing.T) {
	allowed := [][2]Status{
		{StatusPending, StatusCancelled},
		{StatusPaid, StatusProcessing},
		{StatusProcessing, StatusShipped},
		{StatusShipped, StatusCompleted},
	}
	for _, tr := range allowed {
		assert.Truef(t, CanTransition(tr[0], tr[1]), "%s -> %s should be allowed", tr[0], tr[1])
	}

	denied := [][2]Status{
		{StatusPending, StatusPaid},       // only a payment may do this
		{StatusPending, StatusShipped},    // skipping steps
		{StatusPaid, StatusCancelled},     // would need a refund flow
		{StatusPaid, StatusCompleted},     // skipping steps
		{StatusShipped, StatusProcessing}, // going backwards
		{StatusCompleted, StatusShipped},  // terminal
		{StatusCancelled, StatusPending},  // terminal
		{StatusCancelled, StatusCancelled},
	}
	for _, tr := range denied {
		assert.Falsef(t, CanTransition(tr[0], tr[1]), "%s -> %s should be denied", tr[0], tr[1])
	}
}
