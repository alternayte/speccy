-- +goose Up
-- One inbox item a person opened, so a click marks that item read without the others. The key
-- names the item: its kind, its bundle, its thread or waiver, and its time. Rows older than
-- the inbox window no longer match any item.
CREATE TABLE inbox_read (
    user_id  TEXT NOT NULL,
    item_key TEXT NOT NULL,
    read_at  DATETIME NOT NULL,
    PRIMARY KEY (user_id, item_key)
);

-- +goose Down
DROP TABLE inbox_read;
