// Package rules contains the pure Rock-Paper-Scissors game logic: round
// resolution, status transitions and move visibility. It performs no I/O.
package rules

// Move is a player's throw. PENDING is used only in storage.
type Move string

const (
	Rock     Move = "ROCK"
	Paper    Move = "PAPER"
	Scissors Move = "SCISSORS"
	Pending  Move = "PENDING"
)

// Status is the lifecycle status of a game.
type Status string

const (
	StatusWaitingForGuest    Status = "WAITING_FOR_GUEST_TO_JOIN"
	StatusActive             Status = "ACTIVE"
	StatusComplete           Status = "COMPLETE"
	StatusHostIssuedRematch  Status = "HOST_ISSUED_REMATCH"
	StatusGuestIssuedRematch Status = "GUEST_ISSUED_REMATCH"
	StatusClosed             Status = "CLOSED"
)

// GameState is the outcome of the current round.
type GameState string

const (
	StateWaitingForMoves GameState = "WAITING_FOR_MOVES"
	StateDraw            GameState = "DRAW"
	StateHostWon         GameState = "HOST_WON"
	StateGuestWon        GameState = "GUEST_WON"
)

// Seat identifies a participant's position in a game.
type Seat string

const (
	Host  Seat = "HOST"
	Guest Seat = "GUEST"
)

// Other returns the opposing seat.
func (s Seat) Other() Seat {
	if s == Host {
		return Guest
	}
	return Host
}

// Error is a rule violation carrying a protocol error code.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

var (
	ErrInvalidMove          = &Error{"invalid_move", "move must be ROCK, PAPER or SCISSORS"}
	ErrInvalidState         = &Error{"invalid_state", "action not allowed in the current game status"}
	ErrAlreadyLockedIn      = &Error{"already_locked_in", "you have already locked in a move this round"}
	ErrRematchAlreadyIssued = &Error{"rematch_already_issued", "you have already issued a rematch"}
	ErrGameClosed           = &Error{"game_closed", "game is closed"}
)

// ParseMove validates a client-supplied move. PENDING is not a valid throw.
func ParseMove(s string) (Move, error) {
	switch m := Move(s); m {
	case Rock, Paper, Scissors:
		return m, nil
	}
	return "", ErrInvalidMove
}

// beats maps each move to the move it defeats.
var beats = map[Move]Move{Rock: Scissors, Scissors: Paper, Paper: Rock}

// Resolve returns the round outcome for the two locked-in moves.
func Resolve(host, guest Move) GameState {
	switch {
	case host == guest:
		return StateDraw
	case beats[host] == guest:
		return StateHostWon
	default:
		return StateGuestWon
	}
}

// State is the mutable game state the rules operate on.
type State struct {
	Status    Status
	GameState GameState
	HostMove  Move
	GuestMove Move
	HostWins  int
	GuestWins int
	Draws     int
}

// NewState is the state of a freshly created game.
func NewState() State {
	return State{
		Status:    StatusWaitingForGuest,
		GameState: StateWaitingForMoves,
		HostMove:  Pending,
		GuestMove: Pending,
	}
}

// MoveOf returns the stored move for seat.
func (s State) MoveOf(seat Seat) Move {
	if seat == Host {
		return s.HostMove
	}
	return s.GuestMove
}

func issuedBy(seat Seat) Status {
	if seat == Host {
		return StatusHostIssuedRematch
	}
	return StatusGuestIssuedRematch
}

// RematchIssuer returns the seat that issued a pending rematch, if any.
func (s State) RematchIssuer() (Seat, bool) {
	switch s.Status {
	case StatusHostIssuedRematch:
		return Host, true
	case StatusGuestIssuedRematch:
		return Guest, true
	}
	return "", false
}

// Join moves a game waiting for its guest to ACTIVE.
func Join(s State) (State, error) {
	if s.Status != StatusWaitingForGuest {
		return s, ErrInvalidState
	}
	s.Status = StatusActive
	return s, nil
}

// PlayMove locks in seat's move and resolves the round once both are in.
func PlayMove(s State, seat Seat, m Move) (State, error) {
	if _, err := ParseMove(string(m)); err != nil {
		return s, err
	}
	if s.Status != StatusActive {
		return s, ErrInvalidState
	}
	if s.MoveOf(seat) != Pending {
		return s, ErrAlreadyLockedIn
	}
	if seat == Host {
		s.HostMove = m
	} else {
		s.GuestMove = m
	}
	if s.HostMove == Pending || s.GuestMove == Pending {
		return s, nil
	}
	s.GameState = Resolve(s.HostMove, s.GuestMove)
	switch s.GameState {
	case StateHostWon:
		s.HostWins++
	case StateGuestWon:
		s.GuestWins++
	case StateDraw:
		s.Draws++
	}
	s.Status = StatusComplete
	return s, nil
}

// Rematch issues a rematch from a COMPLETE game, or accepts the opponent's
// pending rematch. accepted reports whether a new round started.
func Rematch(s State, seat Seat) (next State, accepted bool, err error) {
	if s.Status == StatusComplete {
		s.Status = issuedBy(seat)
		return s, false, nil
	}
	issuer, ok := s.RematchIssuer()
	if !ok {
		return s, false, ErrInvalidState
	}
	if issuer == seat {
		return s, false, ErrRematchAlreadyIssued
	}
	s.Status = StatusActive
	s.GameState = StateWaitingForMoves
	s.HostMove = Pending
	s.GuestMove = Pending
	return s, true, nil
}

// DeclineRematch declines the opponent's pending rematch.
func DeclineRematch(s State, seat Seat) (State, error) {
	issuer, ok := s.RematchIssuer()
	if !ok || issuer == seat {
		return s, ErrInvalidState
	}
	s.Status = StatusComplete
	return s, nil
}

// Close closes the game (leave or player expiry). CLOSED is terminal.
func Close(s State) (State, error) {
	if s.Status == StatusClosed {
		return s, ErrGameClosed
	}
	s.Status = StatusClosed
	return s, nil
}

// VisibleMove returns the move of owner's seat as seen by viewer. Own moves
// are always visible; the opponent's move is hidden while the round is in
// progress (ACTIVE). ok is false when the move should be shown as null.
func VisibleMove(s State, viewer, owner Seat) (m Move, ok bool) {
	stored := s.MoveOf(owner)
	if stored == Pending || stored == "" {
		return "", false
	}
	if viewer != owner && s.Status == StatusActive {
		return "", false
	}
	return stored, true
}
