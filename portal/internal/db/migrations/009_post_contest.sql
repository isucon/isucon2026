ALTER TABLE instance_command_execute_request_results ALTER COLUMN contestant_instance_id DROP NOT NULL;
ALTER TABLE instance_command_execute_request_results ADD COLUMN router_team_id bigint;
ALTER TABLE instance_command_execute_request_results ADD CONSTRAINT command_target CHECK ((contestant_instance_id IS NULL) <> (router_team_id IS NULL));
CREATE UNIQUE INDEX command_router_target ON instance_command_execute_request_results(instance_command_execute_request_id, router_team_id) WHERE router_team_id IS NOT NULL;
