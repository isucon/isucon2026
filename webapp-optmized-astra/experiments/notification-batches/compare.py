"""Measure notification row versus immutable batch insertion in an owned schema.

Only a local PostgreSQL test database is accepted. No live tables are changed.
"""
import json
import os
from pathlib import Path
import subprocess
import uuid

port = os.environ.get('ASTRA_TEST_PGPORT', '26210')
assert port.isdigit()
schema = 'astra_notification_batch_pilot_' + uuid.uuid4().hex
base = ['psql', '-X', '-qAt', '-v', 'ON_ERROR_STOP=1', '-h', '127.0.0.1', '-p', port, '-U', 'postgres', '-d', 'postgres']

def sql(statement):
    return subprocess.check_output(base, input=statement, text=True)

def query(statement):
    return sql(f'SET search_path TO {schema};\n' + statement)

output = {'recipients': 5000, 'runs': []}
try:
    sql(f'CREATE SCHEMA {schema};')
    query('''
    CREATE TABLE users (id varchar(36) PRIMARY KEY);
    CREATE TABLE windows (id varchar(36) PRIMARY KEY);
    INSERT INTO users SELECT md5(i::text)::uuid::text FROM generate_series(1,5000) i;
    INSERT INTO windows VALUES ('00000000-0000-0000-0000-000000000001');
    CREATE TABLE source AS SELECT id AS user_id, md5('notification:'||id)::uuid::text AS id FROM users ORDER BY id;
    CREATE TABLE individual (
      id varchar(36) PRIMARY KEY,
      user_id varchar(36) NOT NULL REFERENCES users(id),
      posting_window_id varchar(36) NOT NULL REFERENCES windows(id),
      created_at timestamptz NOT NULL,
      UNIQUE(posting_window_id,user_id)
    );
    CREATE INDEX individual_user ON individual(user_id);
    CREATE TABLE batches (
      posting_window_id varchar(36) PRIMARY KEY REFERENCES windows(id),
      user_ids text[] NOT NULL,
      notification_ids text[] NOT NULL,
      created_at timestamptz NOT NULL,
      CHECK (cardinality(user_ids)=cardinality(notification_ids))
    );
    ANALYZE source; ANALYZE users;
    ''')
    for iteration in range(3):
        for mode in ('individual','batches') if iteration % 2 == 0 else ('batches','individual'):
            query(f'TRUNCATE {mode};')
            if mode == 'individual':
                insert = "INSERT INTO individual SELECT id,user_id,'00000000-0000-0000-0000-000000000001',now() FROM source ORDER BY user_id"
            else:
                insert = "INSERT INTO batches SELECT '00000000-0000-0000-0000-000000000001',array_agg(user_id ORDER BY user_id),array_agg(id ORDER BY user_id),now() FROM source"
            plan = json.loads(query('EXPLAIN (ANALYZE,BUFFERS,WAL,FORMAT JSON) '+insert+';'))[0]
            total_bytes = int(query(f"SELECT pg_total_relation_size('{mode}');").strip())
            output['runs'].append({'mode':mode,'iteration':iteration,'relation_bytes':total_bytes,'plan':plan})
    mismatch = int(query('''SELECT count(*) FROM (
      (SELECT user_id,id FROM individual EXCEPT SELECT user_id,id FROM batches b, unnest(b.user_ids,b.notification_ids) AS n(user_id,id))
      UNION ALL
      (SELECT user_id,id FROM batches b, unnest(b.user_ids,b.notification_ids) AS n(user_id,id) EXCEPT SELECT user_id,id FROM individual)
    ) differences;''').strip())
    assert mismatch == 0
    output['all_ids_and_recipients_match'] = True
finally:
    sql(f'DROP SCHEMA IF EXISTS {schema} CASCADE;')
print(json.dumps(output,indent=2))
