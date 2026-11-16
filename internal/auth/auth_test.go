package auth

import (
	"testing"
	"time"
)

func TestTicketExpiresAndIsSingleUse(t *testing.T) {
	now := time.Unix(1000, 0)
	a := New(Tokens{Admin: "a-very-long-admin-token"})
	a.now = func() time.Time { return now }

	tk, ttl, err := a.IssueTicket(Operator)
	if err != nil || ttl != TicketTTL {
		t.Fatal(err, ttl)
	}
	if got := a.redeemTicket(tk); got != Operator {
		t.Fatalf("first use: %v", got)
	}
	if got := a.redeemTicket(tk); got != None {
		t.Fatalf("second use: %v", got)
	}

	late, _, _ := a.IssueTicket(Viewer)
	now = now.Add(TicketTTL + time.Millisecond)
	if got := a.redeemTicket(late); got != None {
		t.Fatalf("expired ticket: %v", got)
	}
	// Issuing prunes expired tickets.
	a.IssueTicket(Viewer)
	now = now.Add(time.Hour)
	a.IssueTicket(Viewer)
	if n := len(a.tickets); n != 1 {
		t.Fatalf("tickets kept: %d", n)
	}
}

func TestTokensMapToRoles(t *testing.T) {
	a := New(Tokens{Admin: "admin-xxxxxxxxxxxx", Operator: "operator-xxxxxxxxx", Viewer: "viewer-xxxxxxxxxxx"})
	for tok, want := range map[string]Role{
		"admin-xxxxxxxxxxxx": Admin, "operator-xxxxxxxxx": Operator, "viewer-xxxxxxxxxxx": Viewer,
		"": None, "admin": None, "admin-xxxxxxxxxxxxx": None,
	} {
		if got := a.roleForToken(tok); got != want {
			t.Errorf("%q: %v want %v", tok, got, want)
		}
	}
	if New(Tokens{}).Enabled() {
		t.Error("no tokens must mean auth off")
	}
}

func TestFailureWindowSlides(t *testing.T) {
	now := time.Unix(0, 0)
	a := New(Tokens{Admin: "x-long-enough-token"})
	a.now = func() time.Time { return now }
	for range MaxAuthFailures {
		a.fail("10.0.0.1")
	}
	if !a.blocked("10.0.0.1") || a.blocked("10.0.0.2") {
		t.Fatal("blocking is per IP")
	}
	now = now.Add(AuthFailureWindow + time.Second)
	if a.blocked("10.0.0.1") {
		t.Fatal("block must expire")
	}
}
