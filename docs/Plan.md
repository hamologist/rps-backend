The purpose of this project is to see to the creation of a webserver that processes and manages game state for rock paper scissors.
# Technology
- Programming language: Go
- Websocket library: [coder/websocket](https://github.com/coder/websocket)
- Database: SQLite
- SQLite DB library: [mattn/go-sqlite3](https://github.com/mattn/go-sqlite3)
# User Stories
## Player Creation
As a player, I need the ability to register an anonymous and temporary player account with the system. I want a dedicated `/players [POST]` endpoint that I can hit where I will provide a username that I want to be identified by. The system should include logic that allows multiple players with the same name to be disambiguated. This should look something like:
1. Player is created with username `hamologist`
2. System creates player with username `hamologist` and a disambiguator of `1234`
3. User appears in the system with name `hamologist#1234`
I expect to receive a secret key that I can use for making future protected player requests.
## Game Creation
As an active player with a valid secret, I need the ability to create a game. The game that I create should automatically have my player associated as player one. I want a dedicated `/games [POST]` endpoint that I can hit. Once the game has been created, I should be able to share the game's id and secret with another player so they can join it. Likewise, I should also be able to use the same game id and secret so I can join the newly created game as well.
## Websocket connection
As a user, I need the ability to connect to the webserver using a websocket on `/ws`. Once the connection completes I should be able to issue commands to the webserver over my websocket using payloads in the form of:
```
{
	action: {{ACTION_ENUM}}
	payload: {
		...
	}
}
```
Where valid `payload` value is dependent on `action`.
## Tie Player to Websocket connection
As a user, once a Websocket connection is established, I need the ability to verify that I am an active player that was created prior. I should not be able to perform other actions at this point other than the [Player Authenticate](#player-authenticate) action. A player should only ever be associated with one connection at a time. If a new connection attempts to associate to the player, the previous connection should be dropped.
## Connect to a Game
As a user who has already opened a Websocket connection and has authenticated as an existing player, I need the ability to associate my connection to a game. I should only be able to have my connection associated to one game at a time. I will do so using the [Game Connect](#game-connect) action.
## Player Makes a Move
As an authenticated player, I need the ability to send my desired move to a game I am associated with. Once my move has been sent, I will be locked in and wait for the other player to send their move. I will send my move using the [Game Move](#game-move) action. Once both moves have been locked in the game's wins and draw counts should be updated accordingly. Likewise, the game state and status should be updated as well.
## Player Rejoins the Game
As an authenticated player, I need the ability to rejoin a game that I was still in the process of playing. I will rejoin the game using the [Game Connect](#game-connect) action.
## Player Issues a New Game After Current Game Has Ended
As an authenticated player associated to a game that has completed, I need the ability to issue a rematch. The other player would have to accept the rematch, otherwise the game would go back to a completed state. If the other player did accept the game state would have to reset to the following:
- `host_player_latest_move` -> `PENDING`
- `guest_player_latest_move` -> `PENDING`
- `status` -> `ACTIVE`
- `game_state` -> `WAITING_FOR_MOVES`
## Player Leaves a Game
As an authenticated player associated to a game, I need the ability to leave said game. I will leave the game using the [Game Leave](#game-leave) action.
## Player Expires
Players accounts are intended to be temporary. Players with `last_active_at` values that are over 24 hours should be scrubbed from the database. Players that are up for deletion should also have any active connections terminated. Games that they are associated to should have their `status` set to `CLOSED`.
## Game Expires
Games are intended to be temporary. Games with `last_active_at` values that are over 24 hours should be scrubbed from the database. Players that are connected to games that are being scrubbed should have their connections terminated.
## Player Last Active At Updates
As a player, I want my `last_active_at` to update whenever I perform actions like:
- [Games Create](#games-create)
- [Player Authenticate](#player-authenticate)
- [Game Connect](#game-connect)
- [Game Move](#game-move)
- [Game Rematch](#game-rematch)
- [Game Leave](#game-leave)
## Game Last Active At Updates
As a user, I want games `last_active_at` to update whenever actions are performed against the game like:
- [Game Connect](#game-connect)
- [Game Move](#game-move)
- [Game Rematch](#game-rematch)
- [Game Leave](#game-leave)
# HTTP Endpoints
## Player Create
- Route: `/players`
- Verb: `POST`
- Payload:
```json
{
	username: string
}
```
- Response payload:
```json
{
	id: string
	discriminator: integer
	display_name: string
	secret: string
}
```
## Games Create
- Route: `/games`
- Verb: `POST`
- Payload:
```json
{
	"player_id": {{string containing Players.id}},
	"player_secret": {{string containing Players.secret}}
}
```
- Response payload:
```json
{
	"id": {{string containing Games.id}}
}
```
## Websocket Connection
- Route: `/ws`
- Verb: `GET`
# Websocket Actions
## Player Authenticate
Only available action after opening a websocket. Ensures that subsequent actions are tied to the provided player.
- Payload:
```json
{
	"action": "player::authenticate",
	"payload": {
		"player_id": {{string containing Players.id}},
		"player_secret": {{string containing Players.secret}}
	}
}
```
## Game Connect
Only available after the connection has been associated to an active player. Ensures that subsequent actions are tied to the provided game. If the host and guest players have already been defined, this action should fail. This action should allow the host or guest to reconnect to a game they may have dropped from.
- Payload:
```json
{
	"action": "game::connect",
	"payload": {
		"game_id": {{string containing Games.id}},
		"game_secret": {{string containing Games.secret}}
	}
}
```
## Game Move
Only available to authenticated players who have connected to a game. Locks the associated player to the move they have provided.
- Payload:
```json
	"action": "game::move",
	"payload": {
		"move": ENUM::MOVES
	}
```
- `MOVES` enum values:
	- `ROCK`
	- `PAPER`
	- `SCISSORS`
## Game Rematch
Only available to authenticated players who have connected to a game and whose game `status` is `COMPLETE`. Alerts the other player that a rematch has been issued and changes the game `status` to either `PLAYER_ONE_ISSUED_REMATCH` or `PLAYER_TWO_ISSUED_REMATCH`.
- Payload:
```json
{
	"action": "game::rematch"
}
```
## Game Leave
Only available to authenticated players who have connected to a game. Disassociates the current game from the current connection. Set's the game's `status` to `CLOSED`. Should alert the other player that the game has been closed.
- Payload:
```json
{
	"action": "game::leave"
}
```
# DB Schema
## Players
Represents anonymous and temporary players
### Fields
#### id
- type: `string`
#### base_name
- type: `string`
#### discriminator
- type: `integer`
#### secret
- type: `string`
#### last_active_at
- type: `string` (ISO Timestamp)
### Uniqueness Constraint
`base_name` + `discriminator`
## Games
### Fields
#### id
- type: `string`
#### secret
- type: `string`
#### host_player_id
- type: `string`
- foreign key to `Players`
#### guest_player_id
- type: `string`
- foreign key to `Players`
#### host_player_latest_move
- type: `enum`
- values:
	- `ROCK`
	- `PAPER`
	- `SCISSORS`
	- `PENDING`
#### guest_player_latest_move
- type: `enum`
- values:
	- `ROCK`
	- `PAPER`
	- `SCISSORS`
	- `PENDING`
#### host_player_wins
- type: `integer`
#### guest_player_wins
- type: `integer`
#### draws
- type: `integer`
#### status
- type: `enum`
- values:
	- `WAITING_FOR_SECOND_PLAYER_TO_JOIN`
	- `ACTIVE`
	- `COMPLETE`
	- `PLAYER_ONE_ISSUED_REMATCH`
	- `PLAYER_TWO_ISSUED_REMATCH`
	- `CLOSED`
#### game_state
- type: `enum`
- values:
	- `WAITING_FOR_MOVES`
	- `DRAW`
	- `PLAYER_ONE_WON`
	- `PLAYER_TWO_WON`
#### last_active_at
- type: `string` (ISO Timestamp)
### Uniqueness Constraint
`id` is sufficient.
