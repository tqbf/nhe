CREATE TABLE IF NOT EXISTS years (
    id INTEGER PRIMARY KEY,
    year INTEGER NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS categories (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    parent_id INTEGER,
    indent_level INTEGER NOT NULL,
    sort_order INTEGER NOT NULL,
    is_major_heading INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (parent_id) REFERENCES categories(id)
);

CREATE TABLE IF NOT EXISTS expenditures (
    id INTEGER PRIMARY KEY,
    category_id INTEGER NOT NULL,
    year_id INTEGER NOT NULL,
    amount INTEGER,
    FOREIGN KEY (category_id) REFERENCES categories(id),
    FOREIGN KEY (year_id) REFERENCES years(id),
    UNIQUE(category_id, year_id)
);
