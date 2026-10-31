-- One line per team whose Router has registered, with its asia server (number 1).
-- NULL columns become empty fields; router-data-proxy-block validates them.
BEGIN READ ONLY;
SELECT format(E'target\t%s\t%s\t%s\t%s\t%s\t%s\t%s',
  r.team_id, r.cloud_id, r.public_ipv4_address, r.benchmark_host,
  i.cloud_id, i.public_ipv4_address, i.status)
FROM team_routers r
LEFT JOIN contestant_instances i ON i.team_id = r.team_id AND i.number = 1
WHERE r.cloud_id IS NOT NULL
ORDER BY r.team_id;
-- list-targets.sh requires this line, so a truncated log is not mistaken for all teams.
SELECT 'targets-complete';
COMMIT;
