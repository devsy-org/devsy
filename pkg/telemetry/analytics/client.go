package analytics

import (
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/log"
	"github.com/posthog/posthog-go"
)

// Cap CLI exit delay on slow networks; dropping a queued event beats
// blocking the user's shell.
const flushTimeout = 2 * time.Second

// Injected at build time via -ldflags -X. Empty in local builds yields a noop client.
var posthogAPIKey = ""

const posthogEndpoint = "https://us.i.posthog.com"

var Dry = false

func NewClient() Client {
	if posthogAPIKey == "" {
		log.Debugf("analytics disabled: API key not configured")
		return NewNoopClient()
	}

	phClient, err := posthog.NewWithConfig(posthogAPIKey, posthog.Config{
		Endpoint:     posthogEndpoint,
		DisableGeoIP: new(false),
	})
	if err != nil {
		log.Debugf("failed to initialize analytics client: %v", err)
		return NewNoopClient()
	}

	return &client{phClient: phClient}
}

type client struct {
	phClient  posthog.Client
	closeOnce sync.Once
}

func (c *client) RecordEvent(event Event) {
	eventData, ok := event["event"]
	if !ok {
		return
	}

	machineID, _ := eventData[KeyMachineID].(string)
	eventType, _ := eventData[KeyType].(string)
	properties := buildProperties(event)

	if Dry {
		log.Infof(
			"analytics event: type=%s machine_id=%s properties=%v",
			eventType, machineID, properties,
		)
		return
	}

	if machineID == "" {
		log.Debugf("skipping event with empty machine_id: %s", eventType)
		return
	}

	if err := c.phClient.Enqueue(posthog.Capture{
		DistinctId: machineID,
		Event:      eventType,
		Properties: properties,
	}); err != nil {
		log.Debugf("error recording analytics event: %v", err)
	}
}

// Exclude reserved keys for event routing/identity.
func isReservedKey(k string) bool {
	return k == KeyType || k == KeyMachineID || k == KeyTimestamp
}

func buildProperties(event Event) posthog.Properties {
	properties := posthog.NewProperties()

	for k, v := range event["event"] {
		if isReservedKey(k) {
			continue
		}
		properties.Set(k, v)
	}

	for k, v := range event["user"] {
		if isReservedKey(k) {
			continue
		}
		properties.Set(k, v)
	}

	return properties
}

func (c *client) Flush() {
	if Dry {
		return
	}
	// The underlying client's Close drains the queue but can only be called once.
	c.closeOnce.Do(func() {
		done := make(chan error, 1)
		go func() { done <- c.phClient.Close() }()
		select {
		case err := <-done:
			if err != nil {
				log.Debugf("error flushing analytics client: %v", err)
			}
		case <-time.After(flushTimeout):
			log.Debugf("analytics flush timed out after %s; dropping queued events", flushTimeout)
		}
	})
}
