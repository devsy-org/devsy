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
		log.Debugf("PostHog API key not configured; analytics disabled")
		return NewNoopClient()
	}

	phClient, err := posthog.NewWithConfig(posthogAPIKey, posthog.Config{
		Endpoint: posthogEndpoint,
	})
	if err != nil {
		log.Debugf("failed to create PostHog client: %v", err)
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

	machineID, _ := eventData["machine_id"].(string)
	eventType, _ := eventData["type"].(string)
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
		log.Debugf("error enqueuing PostHog event: %v", err)
	}
}

func buildProperties(event Event) posthog.Properties {
	properties := posthog.NewProperties()

	for k, v := range event["event"] {
		if k == "machine_id" || k == "timestamp" {
			continue
		}
		properties.Set(k, v)
	}

	for k, v := range event["user"] {
		if k == "machine_id" || k == "timestamp" {
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
	// posthog-go's Close drains the queue but can only be called once.
	c.closeOnce.Do(func() {
		done := make(chan error, 1)
		go func() { done <- c.phClient.Close() }()
		select {
		case err := <-done:
			if err != nil {
				log.Debugf("error flushing PostHog client: %v", err)
			}
		case <-time.After(flushTimeout):
			log.Debugf("PostHog flush timed out after %s; dropping queued events", flushTimeout)
		}
	})
}
