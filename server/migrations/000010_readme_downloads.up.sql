-- The registry stores no bytes, so it has neither a README nor a download
-- count of its own. Both come from the publisher's GitHub release, and both
-- are cached here: a page view must not depend on GitHub answering, and a
-- chart needs history GitHub does not keep.

-- One cached README per release, read from the repository at the release's
-- tag. status is the outcome of the last attempt, not a claim about the
-- markdown: a fetch that fails leaves the markdown it already had, so a GitHub
-- outage shows the last good README instead of an empty page. 'missing' means
-- the source has no README, or the artifacts are not GitHub release assets.
CREATE TABLE IF NOT EXISTS release_readmes (
    release_id uuid        PRIMARY KEY
                           REFERENCES releases (id)
                           ON DELETE CASCADE
                           ON UPDATE CASCADE,
    markdown   text        NOT NULL DEFAULT '',
    source_url text        NOT NULL DEFAULT '',
    status     text        NOT NULL,
    -- When the markdown was last read. Null while it has never been read.
    fetched_at timestamptz,
    -- When GitHub was last asked, whatever the answer. Freshness is measured
    -- from here, so a repeated failure is not retried on every page view.
    checked_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT release_readmes_status_known CHECK (status IN ('ok', 'missing', 'unavailable'))
);

-- GitHub reports a cumulative download count per release asset and keeps no
-- history, so the chart is built from daily samples of that counter. One row
-- per artifact per day; the first day an artifact is sampled has no
-- predecessor and so contributes no bar.
CREATE TABLE IF NOT EXISTS download_snapshots (
    artifact_id uuid   NOT NULL
                       REFERENCES artifacts (id)
                       ON DELETE CASCADE
                       ON UPDATE CASCADE,
    day         date   NOT NULL,
    count       bigint NOT NULL,

    PRIMARY KEY (artifact_id, day),

    CONSTRAINT download_snapshots_count_not_negative CHECK (count >= 0)
);

CREATE INDEX IF NOT EXISTS download_snapshots_day_idx
    ON download_snapshots (day);
