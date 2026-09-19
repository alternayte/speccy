-- +goose Up
-- REQ-040, REQ-047: build questions, pinned per bundle version.
CREATE TABLE question (
    id           UUIDTEXT PRIMARY KEY,
    workspace_id UUIDTEXT NOT NULL REFERENCES workspace (id),
    bundle_id    UUIDTEXT NOT NULL REFERENCES bundle (id),
    version_id   UUIDTEXT NOT NULL REFERENCES version (id),
    number       INTEGER NOT NULL CHECK (number > 0),
    text         TEXT NOT NULL,
    level        TEXT NOT NULL CHECK (level IN ('MUST', 'SHOULD')),
    cites        JSONTEXT NOT NULL,
    anchor       JSONTEXT NOT NULL,
    UNIQUE (version_id, number)
);

-- REQ-042, REQ-043: one reader's answer to one question in one run.
CREATE TABLE answer (
    question_id       UUIDTEXT NOT NULL REFERENCES question (id),
    run_id            UUIDTEXT NOT NULL REFERENCES review_run (id),
    reader_role       TEXT NOT NULL,
    model_fingerprint TEXT NOT NULL,
    answer            TEXT NOT NULL,
    quotes            JSONTEXT NOT NULL,
    quotes_found      BOOLEAN NOT NULL,
    PRIMARY KEY (run_id, question_id, reader_role)
);

-- REQ-044: the judge's groups and the classification of one question in one run.
CREATE TABLE question_result (
    run_id      UUIDTEXT NOT NULL REFERENCES review_run (id),
    question_id UUIDTEXT NOT NULL REFERENCES question (id),
    result      TEXT NOT NULL CHECK (result IN ('agree', 'diverge', 'gap')),
    groups      JSONTEXT NOT NULL,
    PRIMARY KEY (run_id, question_id)
);

-- +goose Down
DROP TABLE question_result;
DROP TABLE answer;
DROP TABLE question;
