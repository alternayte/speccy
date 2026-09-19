-- +goose Up
-- REQ-040, REQ-047: build questions, pinned per bundle version.
CREATE TABLE question (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspace (id),
    bundle_id    uuid NOT NULL REFERENCES bundle (id),
    version_id   uuid NOT NULL REFERENCES version (id),
    number       bigint NOT NULL CHECK (number > 0),
    text         text NOT NULL,
    level        text NOT NULL CHECK (level IN ('MUST', 'SHOULD')),
    cites        jsonb NOT NULL,
    anchor       jsonb NOT NULL,
    UNIQUE (version_id, number)
);

-- REQ-042, REQ-043: one reader's answer to one question in one run.
CREATE TABLE answer (
    question_id       uuid NOT NULL REFERENCES question (id),
    run_id            uuid NOT NULL REFERENCES review_run (id),
    reader_role       text NOT NULL,
    model_fingerprint text NOT NULL,
    answer            text NOT NULL,
    quotes            jsonb NOT NULL,
    quotes_found      boolean NOT NULL,
    PRIMARY KEY (run_id, question_id, reader_role)
);

-- REQ-044: the judge's groups and the classification of one question in one run.
CREATE TABLE question_result (
    run_id      uuid NOT NULL REFERENCES review_run (id),
    question_id uuid NOT NULL REFERENCES question (id),
    result      text NOT NULL CHECK (result IN ('agree', 'diverge', 'gap')),
    groups      jsonb NOT NULL,
    PRIMARY KEY (run_id, question_id)
);

-- +goose Down
DROP TABLE question_result;
DROP TABLE answer;
DROP TABLE question;
