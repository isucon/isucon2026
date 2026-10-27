fn default_hard_timeout() -> u64 {
    210
}

fn default_log_directory() -> String {
    "/tmp".to_string()
}

fn default_interval_after_empty_receive() -> u64 {
    5
}

#[derive(serde::Deserialize)]
pub struct Config {
    pub endpoint_url: String,
    pub token: String,
    pub team_id: Option<i64>,
    pub instance_name: String,
    #[serde(default = "default_log_directory")]
    pub log_directory: String,
    #[serde(default = "default_hard_timeout")]
    pub hard_timeout: u64,
    #[serde(default = "default_interval_after_empty_receive")]
    pub interval_after_empty_receive: u64,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn default_budget_covers_benchmark_timeout() {
        assert!(default_hard_timeout() > 190);
    }
}

impl std::fmt::Debug for Config {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Config")
            .field("endpoint_url", &self.endpoint_url)
            .field("token", &"[REDACTED]")
            .field("team_id", &self.team_id)
            .field("instance_name", &self.instance_name)
            .field("log_directory", &self.log_directory)
            .field("hard_timeout", &self.hard_timeout)
            .field("interval_after_empty_receive", &self.interval_after_empty_receive)
            .finish()
    }
}
