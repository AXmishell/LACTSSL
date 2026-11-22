package model

import (
	"testing"
	"time"
)

func TestSecurityEventDeliveryState(t *testing.T) {
	now := time.Now().UTC()
	deliveredAt := now
	recipients := []string{"admin@example.com", "ops@example.com"}

	cases := []struct {
		name  string
		event SecurityEvent
		want  string
	}{
		{
			name:  "recorded",
			event: SecurityEvent{Notify: false},
			want:  SecurityEventStateRecorded,
		},
		{
			name: "pending before first attempt",
			event: SecurityEvent{
				Notify:        true,
				Recipients:    recipients,
				Channels:      []string{"mail"},
				NextAttemptAt: now,
			},
			want: SecurityEventStatePending,
		},
		{
			name: "retrying after partial failure",
			event: SecurityEvent{
				Notify:        true,
				Recipients:    recipients,
				Channels:      []string{"mail"},
				Deliveries:    map[string]string{"mail:admin@example.com": "sent", "mail:ops@example.com": "failed"},
				Attempts:      1,
				LastError:     "ops mailbox down",
				NextAttemptAt: now.Add(time.Minute),
			},
			want: SecurityEventStateRetrying,
		},
		{
			name: "retrying after crash with failed progress",
			event: SecurityEvent{
				Notify:        true,
				Recipients:    recipients,
				Channels:      []string{"mail"},
				Deliveries:    map[string]string{"mail:admin@example.com": "sent", "mail:ops@example.com": "failed"},
				NextAttemptAt: now.Add(time.Minute),
			},
			want: SecurityEventStateRetrying,
		},
		{
			name: "terminal failure",
			event: SecurityEvent{
				Notify:     true,
				Recipients: recipients,
				Channels:   []string{"mail"},
				Deliveries: map[string]string{"mail:admin@example.com": "sent", "mail:ops@example.com": "failed"},
				Attempts:   3,
				LastError:  "ops mailbox down",
			},
			want: SecurityEventStateFailed,
		},
		{
			name: "delivered_at wins",
			event: SecurityEvent{
				Notify:      true,
				Recipients:  recipients,
				Channels:    []string{"mail"},
				Deliveries:  map[string]string{"mail:admin@example.com": "sent"},
				DeliveredAt: &deliveredAt,
			},
			want: SecurityEventStateDelivered,
		},
		{
			name: "all targets sent without delivered_at",
			event: SecurityEvent{
				Notify:     true,
				Recipients: recipients,
				Channels:   []string{"mail", "telegram"},
				Deliveries: map[string]string{
					"mail:admin@example.com": "sent",
					"mail:ops@example.com":   "sent",
					"channel:telegram":       "sent",
				},
				Attempts: 1,
			},
			want: SecurityEventStateDelivered,
		},
		{
			name: "skipped channel is not required",
			event: SecurityEvent{
				Notify:     true,
				Recipients: []string{"admin@example.com"},
				Channels:   []string{"mail", "telegram"},
				Deliveries: map[string]string{
					"mail:admin@example.com": "sent",
					"channel:telegram":       "skipped",
				},
				Attempts: 1,
			},
			want: SecurityEventStateDelivered,
		},
		{
			name: "no available target",
			event: SecurityEvent{
				Notify:    true,
				Attempts:  1,
				LastError: "no recipient or channel",
			},
			want: SecurityEventStateFailed,
		},
	}

	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if got := item.event.DeliveryState(); got != item.want {
				t.Fatalf("状态不正确: got=%s want=%s event=%+v", got, item.want, item.event)
			}
		})
	}
}
