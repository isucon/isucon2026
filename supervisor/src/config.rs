fn default_hard_timeout() -> u64 {
    210
}

fn default_log_directory() -> String {
    "/tmp".to_string()
}

fn default_interval_after_empty_receive() -> u64 {
    5
}

#[derive(Clone, Copy, Debug, Default, serde::Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum BenchmarkClass {
    #[default]
    Standard,
    Large,
}

impl BenchmarkClass {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Standard => "standard",
            Self::Large => "large",
        }
    }
}

#[derive(serde::Deserialize)]
pub struct Config {
    pub endpoint_url: String,
    #[serde(default)]
    pub benchmark_class: BenchmarkClass,
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
            .field("benchmark_class", &self.benchmark_class)
            .field("token", &"[REDACTED]")
            .field("team_id", &self.team_id)
            .field("instance_name", &self.instance_name)
            .field("log_directory", &self.log_directory)
            .field("hard_timeout", &self.hard_timeout)
            .field("interval_after_empty_receive", &self.interval_after_empty_receive)
            .finish()
    }
}

#[cfg(test)]
mod benchmark_class_tests {
    use super::*;

    #[test]
    fn benchmark_class_defaults_and_rejects_typos() {
        let base =
            vec![("endpoint_url", "https://portal.example.com"), ("token", "test"), ("instance_name", "")];
        let parse = |pairs: Vec<(&str, &str)>| {
            envy::from_iter::<_, Config>(pairs.into_iter().map(|(k, v)| (k.to_owned(), v.to_owned())))
        };
        assert_eq!(parse(base.clone()).unwrap().benchmark_class.as_str(), "standard");
        let mut large = base.clone();
        large.push(("benchmark_class", "large"));
        assert_eq!(parse(large).unwrap().benchmark_class.as_str(), "large");
        let mut invalid = base;
        invalid.push(("benchmark_class", "typo"));
        assert!(parse(invalid).is_err());
    }
}
