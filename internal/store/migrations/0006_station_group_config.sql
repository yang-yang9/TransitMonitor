CREATE TABLE IF NOT EXISTS station_group_config (
    station_id  TEXT    NOT NULL,
    group_name  TEXT    NOT NULL,
    visible     INTEGER NOT NULL DEFAULT 1,
    sort_order  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (station_id, group_name),
    FOREIGN KEY (station_id) REFERENCES stations(id) ON DELETE CASCADE
);
