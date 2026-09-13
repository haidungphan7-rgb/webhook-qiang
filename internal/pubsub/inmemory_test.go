package pubsub_test

import (
	"testing"

	"github.com/yuandzhang/webhook-zq/internal/pubsub"
)

func TestInMemory_Publish_and_Receive(t *testing.T) {
	t.Parallel()

	testPublishAndReceive(t, func() pubSub[any] { return pubsub.NewInMemory[any]() })
}

func TestInMemory_RaceProvocation(t *testing.T) {
	t.Parallel()

	testRaceProvocation(t, func() pubSub[any] { return pubsub.NewInMemory[any]() })
}
