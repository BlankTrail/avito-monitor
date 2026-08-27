// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
)

// TestTheOutboxCarriesItsAttachmentToTheTransport.
//
// notify_outbox.attachment has existed since migration 0001 and nothing read it
// until the inline button. Adding the column to the SELECT without moving the
// Scan destination is exactly the mistake this pins: bodies and attachments
// swapped, and every message went out empty.
func TestTheOutboxCarriesItsAttachmentToTheTransport(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	target, err := s.SaveTarget(ctx, TargetRow{
		Name: "я", Kind: "telegram", Address: "100500", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Enqueue(ctx, OutboxRow{
		TargetID: target, CreatedAt: 1000, DueAt: 1000,
		Body: "объявление выложено заново", Attachment: `{"button_text":"нет"}`,
	}); err != nil {
		t.Fatal(err)
	}
	due, err := s.DueMessages(ctx, 2000, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 {
		t.Fatalf("%d due", len(due))
	}
	if due[0].Body != "объявление выложено заново" {
		t.Errorf("Body = %q", due[0].Body)
	}
	if due[0].Attachment != `{"button_text":"нет"}` {
		t.Errorf("Attachment = %q", due[0].Attachment)
	}
}

// TestOnlyEnabledTelegramChatsMayPressAButton.
//
// The whole of the callback authentication. A disabled recipient must not be
// able to correct the product from a message it was sent before it was
// silenced.
func TestOnlyEnabledTelegramChatsMayPressAButton(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	for _, c := range []struct {
		address string
		enabled bool
	}{
		{"100500", true}, {"999", false}, {"не-число", true},
	} {
		if _, err := s.SaveTarget(ctx, TargetRow{
			Name: c.address, Kind: "telegram", Address: c.address, Enabled: c.enabled,
		}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.TelegramChats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !got[100500] {
		t.Error("an enabled recipient may not press")
	}
	if got[999] {
		t.Error("a silenced recipient may press")
	}
	if len(got) != 1 {
		t.Errorf("chats = %v; an address that is not a number is not a chat", got)
	}
}

// TestTheCallbackOffsetNeverGoesBackwards.
//
// A lower offset asks Telegram to re-deliver, and this is written from a loop
// whose whole job is to stop it doing that.
func TestTheCallbackOffsetNeverGoesBackwards(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	if err := s.SetCallbackOffset(ctx, 50); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCallbackOffset(ctx, 10); err != nil {
		t.Fatal(err)
	}
	got, err := s.CallbackOffset(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != 50 {
		t.Errorf("offset = %d, want it to have stayed at 50", got)
	}
}
