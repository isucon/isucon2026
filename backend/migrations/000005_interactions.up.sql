CREATE TABLE realmojis(
 id uuid PRIMARY KEY,post_id uuid NOT NULL REFERENCES posts(id) ON DELETE RESTRICT,reactor_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,image_id uuid NOT NULL REFERENCES image_objects(id) ON DELETE RESTRICT,reaction_type text NOT NULL CHECK(reaction_type IN('like','smile','surprise','heart','laugh','instant')),created_at timestamptz NOT NULL,replaced_at timestamptz,deleted_at timestamptz
);
CREATE UNIQUE INDEX realmojis_one_active ON realmojis(post_id,reactor_id) WHERE replaced_at IS NULL AND deleted_at IS NULL;
CREATE INDEX realmojis_list ON realmojis(post_id,created_at DESC,id DESC) WHERE replaced_at IS NULL AND deleted_at IS NULL;
CREATE TABLE comments(
 id uuid PRIMARY KEY,post_id uuid NOT NULL REFERENCES posts(id) ON DELETE RESTRICT,author_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,body text NOT NULL CHECK(char_length(body) BETWEEN 1 AND 1000),created_at timestamptz NOT NULL,deleted_at timestamptz
);
CREATE INDEX comments_list ON comments(post_id,created_at,id) WHERE deleted_at IS NULL;
