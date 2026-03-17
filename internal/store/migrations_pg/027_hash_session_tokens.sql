-- +goose Up
-- Force re-login: clear all sessions so new ones use hashed tokens.
DELETE FROM ui_sessions;

-- +goose Down
-- Cannot restore original plaintext tokens; sessions are short-lived anyway.
DELETE FROM ui_sessions;
