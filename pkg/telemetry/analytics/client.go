package analytics

import (
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/posthog/posthog-go"
)

const (
	posthogAPIKey = "phc_u3TY39zxfrRcyXJoqZ5WRFVTr75gZBHi2AUrfqJ6GCj2"

	posthogEndpoint = "https://us.i.posthog.com"
)

var Dry = false

func NewClient() Client {
	if posthogAPIKey == "" || posthogAPIKey == "phc_PLACEHOLDER" {
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
	phClient posthog.Client
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
	if err := c.phClient.Close(); err != nil {
		log.Debugf("error flushing PostHog client: %v", err)
	}
}
