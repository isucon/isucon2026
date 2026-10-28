-- Router endpoints are internal benchmark routing data, never contestant servers.
-- A row with no address marks a CloudFormation environment waiting for its Router.
CREATE TABLE IF NOT EXISTS team_routers (
  team_id integer PRIMARY KEY,
  cloud_id varchar(255),
  private_ipv4_address varchar(255),
  public_ipv4_address varchar(255),
  updated_at timestamptz NOT NULL DEFAULT now()
);
