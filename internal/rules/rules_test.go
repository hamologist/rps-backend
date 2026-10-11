package rules

import (
	"errors"
	"testing"
)

func TestResolveAllPairs(t *testing.T) {
	tests := []struct {
		host, guest Move
		want        GameState
	}{
		{Rock, Rock, StateDraw},
		{Rock, Paper, StateGuestWon},
		{Rock, Scissors, StateHostWon},
		{Paper, Rock, StateHostWon},
		{Paper, Paper, StateDraw},
		{Paper, Scissors, StateGuestWon},
		{Scissors, Rock, StateGuestWon},
		{Scissors, Paper, StateHostWon},
		{Scissors, Scissors, StateDraw},
	}
	for _, tt := range tests {
		if got := Resolve(tt.host, tt.guest); got != tt.want {
			t.Errorf("Resolve(%s,%s)=%s want %s", tt.host, tt.guest, got, tt.want)
		}
	}
}

func TestParseMove(t *testing.T) {
	for _, s := range []string{"ROCK", "PAPER", "SCISSORS"} {
		if _, err := ParseMove(s); err != nil {
			t.Errorf("ParseMove(%q) err %v", s, err)
		}
	}
	for _, s := range []string{"", "PENDING", "rock", "LIZARD"} {
		if _, err := ParseMove(s); !errors.Is(err, ErrInvalidMove) {
			t.Errorf("ParseMove(%q) err %v", s, err)
		}
	}
}

func active() State {
	s, err := Join(NewState())
	if err != nil {
		panic(err)
	}
	return s
}

func TestJoin(t *testing.T) {
	s := active()
	if s.Status != StatusActive {
		t.Fatalf("status %s", s.Status)
	}
	if _, err := Join(s); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("join active: %v", err)
	}
}

func TestPlayMoveRound(t *testing.T) {
	tests := []struct {
		name               string
		host, guest        Move
		state              GameState
		hostW, guestW, drw int
	}{
		{"host wins", Rock, Scissors, StateHostWon, 1, 0, 0},
		{"guest wins", Rock, Paper, StateGuestWon, 0, 1, 0},
		{"draw", Paper, Paper, StateDraw, 0, 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := PlayMove(active(), Guest, tt.guest)
			if err != nil {
				t.Fatal(err)
			}
			if s.Status != StatusActive || s.GuestMove != tt.guest || s.HostMove != Pending {
				t.Fatalf("after first move: %+v", s)
			}
			s, err = PlayMove(s, Host, tt.host)
			if err != nil {
				t.Fatal(err)
			}
			if s.Status != StatusComplete || s.GameState != tt.state ||
				s.HostWins != tt.hostW || s.GuestWins != tt.guestW || s.Draws != tt.drw {
				t.Fatalf("after resolve: %+v", s)
			}
			if s.HostMove != tt.host || s.GuestMove != tt.guest {
				t.Fatalf("moves should stay revealed: %+v", s)
			}
		})
	}
}

func TestPlayMoveErrors(t *testing.T) {
	s, _ := PlayMove(active(), Host, Rock)
	if _, err := PlayMove(s, Host, Paper); !errors.Is(err, ErrAlreadyLockedIn) {
		t.Errorf("double move: %v", err)
	}
	if _, err := PlayMove(active(), Host, Pending); !errors.Is(err, ErrInvalidMove) {
		t.Errorf("pending move: %v", err)
	}
	for _, st := range []Status{StatusWaitingForGuest, StatusComplete, StatusHostIssuedRematch, StatusGuestIssuedRematch, StatusClosed} {
		s := active()
		s.Status = st
		if _, err := PlayMove(s, Host, Rock); !errors.Is(err, ErrInvalidState) {
			t.Errorf("move in %s: %v", st, err)
		}
	}
}

func complete() State {
	s, _ := PlayMove(active(), Host, Rock)
	s, _ = PlayMove(s, Guest, Scissors)
	return s
}

func TestRematchTransitions(t *testing.T) {
	// Issue.
	s, accepted, err := Rematch(complete(), Guest)
	if err != nil || accepted || s.Status != StatusGuestIssuedRematch {
		t.Fatalf("issue: %+v %v %v", s, accepted, err)
	}
	// Issuer re-sends.
	if _, _, err := Rematch(s, Guest); !errors.Is(err, ErrRematchAlreadyIssued) {
		t.Fatalf("reissue: %v", err)
	}
	// Issuer can't decline own.
	if _, err := DeclineRematch(s, Guest); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("self decline: %v", err)
	}
	// Accept.
	acc, accepted, err := Rematch(s, Host)
	if err != nil || !accepted {
		t.Fatalf("accept: %v %v", accepted, err)
	}
	if acc.Status != StatusActive || acc.GameState != StateWaitingForMoves ||
		acc.HostMove != Pending || acc.GuestMove != Pending || acc.HostWins != 1 {
		t.Fatalf("accept state: %+v", acc)
	}
	// Decline.
	dec, err := DeclineRematch(s, Host)
	if err != nil || dec.Status != StatusComplete || dec.HostMove != Rock {
		t.Fatalf("decline: %+v %v", dec, err)
	}
	// Host-issued variant.
	h, _, _ := Rematch(complete(), Host)
	if h.Status != StatusHostIssuedRematch {
		t.Fatalf("host issue: %s", h.Status)
	}
	if _, _, err := Rematch(h, Host); !errors.Is(err, ErrRematchAlreadyIssued) {
		t.Fatalf("host reissue: %v", err)
	}
}

func TestRematchIllegal(t *testing.T) {
	for _, st := range []Status{StatusWaitingForGuest, StatusActive, StatusClosed} {
		s := active()
		s.Status = st
		if _, _, err := Rematch(s, Host); !errors.Is(err, ErrInvalidState) {
			t.Errorf("rematch in %s: %v", st, err)
		}
	}
	for _, st := range []Status{StatusWaitingForGuest, StatusActive, StatusComplete, StatusClosed} {
		s := active()
		s.Status = st
		if _, err := DeclineRematch(s, Host); !errors.Is(err, ErrInvalidState) {
			t.Errorf("decline in %s: %v", st, err)
		}
	}
}

func TestClose(t *testing.T) {
	for _, st := range []Status{StatusWaitingForGuest, StatusActive, StatusComplete, StatusHostIssuedRematch, StatusGuestIssuedRematch} {
		s := active()
		s.Status = st
		c, err := Close(s)
		if err != nil || c.Status != StatusClosed {
			t.Errorf("close from %s: %v", st, err)
		}
	}
	s := active()
	s.Status = StatusClosed
	if _, err := Close(s); !errors.Is(err, ErrGameClosed) {
		t.Errorf("close closed: %v", err)
	}
}

func TestVisibleMove(t *testing.T) {
	base := active()
	base.HostMove = Rock
	tests := []struct {
		status        Status
		viewer, owner Seat
		guestMove     Move
		wantOK        bool
	}{
		{StatusActive, Host, Host, Pending, true},   // own move visible
		{StatusActive, Guest, Host, Pending, false}, // opponent hidden while active
		{StatusActive, Host, Guest, Pending, false}, // pending is null
		{StatusComplete, Guest, Host, Paper, true},  // revealed after resolution
		{StatusHostIssuedRematch, Guest, Host, Paper, true},
		{StatusClosed, Guest, Host, Pending, true},
		{StatusClosed, Host, Guest, Pending, false}, // pending stays null
	}
	for i, tt := range tests {
		s := base
		s.Status = tt.status
		s.GuestMove = tt.guestMove
		m, ok := VisibleMove(s, tt.viewer, tt.owner)
		if ok != tt.wantOK {
			t.Errorf("%d: ok=%v want %v", i, ok, tt.wantOK)
		}
		if ok && m != s.MoveOf(tt.owner) {
			t.Errorf("%d: move %s", i, m)
		}
	}
}
