CREATE TABLE users(id text PRIMARY KEY,username text NOT NULL,secret_key_hash text NOT NULL,
          display_name text NOT NULL DEFAULT '',bio text NOT NULL DEFAULT '',profile_image_id text,
          region text NOT NULL DEFAULT 'asia',account_type text NOT NULL DEFAULT 'normal',status text NOT NULL DEFAULT 'active');
        CREATE TABLE posting_windows(id text PRIMARY KEY,local_date date NOT NULL,on_time_deadline timestamptz NOT NULL,ended_at timestamptz);
        CREATE TABLE posts(id text PRIMARY KEY,author_id text NOT NULL,posting_window_id text NOT NULL,image_id text NOT NULL,
          caption text NOT NULL DEFAULT '',visibility text NOT NULL DEFAULT 'public',location_kind text NOT NULL DEFAULT 'none',
          latitude numeric,longitude numeric,locality text,posted_at timestamptz NOT NULL,deleted_at timestamptz);
        CREATE INDEX ON posts(author_id);
        CREATE TABLE comments(id text PRIMARY KEY,post_id text,author_id text,body text,created_at timestamptz,deleted_at timestamptz);
        CREATE TABLE realmojis(id text PRIMARY KEY,post_id text,reactor_id text,image_id text,reaction_type text,created_at timestamptz,replaced_at timestamptz,deleted_at timestamptz);
        CREATE TABLE image_objects(id text,owner_user_id text,data bytea,asset_key text,created_at timestamptz,
          PRIMARY KEY(id,created_at)) PARTITION BY RANGE(created_at);
        CREATE TABLE image_objects_202601 PARTITION OF image_objects FOR VALUES FROM ('2026-01-01') TO ('2026-02-01');
        INSERT INTO users(id,username,secret_key_hash) VALUES('owner','owner',repeat('ab',32)),('friend','friend',repeat('cd',32));
        INSERT INTO posting_windows VALUES('w','2026-01-01','2026-01-01T01:00Z',NULL);
        INSERT INTO posts(id,author_id,posting_window_id,image_id,posted_at)
          SELECT 'p'||lpad(n::text,3,'0'),'owner','w','i'||n,'2026-01-01T00:00Z'::timestamptz+n*interval '1 minute' FROM generate_series(1,55) n;
        INSERT INTO image_objects SELECT image_id,'owner',''::bytea,'pack:original:'||image_id,posted_at FROM posts;
        INSERT INTO comments SELECT 'c'||n,'p055','friend','body'||n,'2026-01-01T00:00Z'::timestamptz+n*interval '1 second',NULL FROM generate_series(1,55) n;
        INSERT INTO realmojis SELECT 'r'||n,'p055','friend','rimg'||n,'smile','2026-01-01T00:00Z'::timestamptz+n*interval '1 second',NULL,NULL FROM generate_series(1,55) n;
