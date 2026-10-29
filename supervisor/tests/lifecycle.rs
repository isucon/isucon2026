use hyper::{
    service::{make_service_fn, service_fn},
    Body, Request, Response, Server,
};
use isuxportal_supervisor::{
    api::{
        self,
        isuxportal::proto::{resources::BenchmarkResult, services::bench::*},
    },
    config::Config,
    worker::Worker,
};
use prost::Message;
use std::{
    convert::Infallible,
    sync::{Arc, Mutex},
};

// Actual child process and inherited FD, with a protobuf HTTP portal double.
#[tokio::test]
async fn receive_progress_complete_through_real_pipe() {
    run_case("pass").await;
}

#[tokio::test]
async fn nonzero_exit_after_pass_reports_failure() {
    run_case("exit-error").await;
}

#[tokio::test]
async fn no_report_cannot_pass() {
    run_case("no-report").await;
}

#[tokio::test]
async fn malformed_report_completes_as_failure() {
    run_case("malformed").await;
}

async fn run_case(case: &str) {
    let reports = Arc::new(Mutex::new(Vec::<BenchmarkResult>::new()));
    let completion = Arc::new(Mutex::new(None::<BenchmarkResult>));
    let captured_reports = reports.clone();
    let captured_completion = completion.clone();
    let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    listener.set_nonblocking(true).unwrap();
    let addr = listener.local_addr().unwrap();
    let server = Server::from_tcp(listener).unwrap();
    let service = make_service_fn(move |_| {
        let reports = captured_reports.clone();
        let completion = captured_completion.clone();
        async move {
            Ok::<_, Infallible>(service_fn(move |req: Request<Body>| {
                let reports = reports.clone();
                let completion = completion.clone();
                async move {
                    let path = req.uri().path().to_owned();
                    if path == "/api/bench/receive" {
                        assert_eq!(req.headers().get("X-Isux-Benchmark-Class").unwrap(), "large");
                    }
                    let bytes = hyper::body::to_bytes(req.into_body()).await.unwrap();
                    let body = match path.as_str() {
                        "/api/bench/receive" => {
                            let req = ReceiveBenchmarkJobRequest::decode(bytes).unwrap();
                            assert_eq!(req.token, "test-token");
                            ReceiveBenchmarkJobResponse {
                                job_handle: Some(receive_benchmark_job_response::JobHandle {
                                    job_id: 42,
                                    handle: "test-handle".into(),
                                    target_ipv4_address: "127.0.0.1".into(),
                                    all_ipv4_addresses: vec![
                                        "127.0.0.1".into(),
                                        "127.0.0.2".into(),
                                        "127.0.0.3".into(),
                                        "127.0.0.4".into(),
                                    ],
                                    ..Default::default()
                                }),
                            }
                            .encode_to_vec()
                        }
                        "/api/bench/benchmark_results" => {
                            let req = ReportBenchmarkResultRequest::decode(bytes).unwrap();
                            assert_eq!(req.handle, "test-handle");
                            reports.lock().unwrap().push(req.result.unwrap());
                            vec![]
                        }
                        "/api/bench/benchmark_results/completion" => {
                            let req = CompleteBenchmarkJobRequest::decode(bytes).unwrap();
                            assert_eq!(req.job_id, 42);
                            assert_eq!(req.handle, "test-handle");
                            *completion.lock().unwrap() = req.result;
                            vec![]
                        }
                        _ => panic!("unexpected route {path}"),
                    };
                    Ok::<_, Infallible>(Response::new(Body::from(body)))
                }
            }))
        }
    });
    let server_task = tokio::spawn(server.serve(service));
    let client = api::Client::new(format!("http://{addr}").parse().unwrap())
        .with_benchmark_class(isuxportal_supervisor::config::BenchmarkClass::Large);
    let job = client
        .receive_benchmark_job(&ReceiveBenchmarkJobRequest {
            token: "test-token".into(),
            instance_name: "apne1-az1".into(),
            team_id: 1,
        })
        .await
        .unwrap()
        .job_handle
        .unwrap();
    let dir = std::env::temp_dir().join(format!("supervisor-test-{}-{case}", std::process::id()));
    std::fs::create_dir_all(&dir).unwrap();
    let config = Config {
        benchmark_class: isuxportal_supervisor::config::BenchmarkClass::Large,
        endpoint_url: format!("http://{addr}"),
        token: "test-token".into(),
        team_id: Some(1),
        instance_name: "apne1-az1".into(),
        log_directory: dir.to_str().unwrap().into(),
        hard_timeout: 5,
        interval_after_empty_receive: 1,
    };
    // frame: length=44, finished=true, passed=true, score=29, survey=go/human/manual/model-a,model-b. Exit immediately to
    // exercise the race between process exit and draining the final report.
    let script = r#"test "$ISUXBENCH_TARGET" = 127.0.0.1 || exit 2
 test "$ISUXBENCH_ALL_ADDRESSES" = 127.0.0.1,127.0.0.2,127.0.0.3,127.0.0.4 || exit 2
 eval "printf '\000\054\010\001\020\001\030\035\102\044\012\002go\022\005human\032\006manual\042\017model-a,model-b' >&$ISUXBENCH_REPORT_FD"
 echo public-output
 echo private-output >&2"#;
    let script = match case {
        "exit-error" => format!("{script}\nexit 1"),
        "no-report" => "exit 0".to_string(),
        "malformed" => r#"eval "printf '\000\001\377' >&$ISUXBENCH_REPORT_FD""#.to_string(),
        _ => script.to_string(),
    };
    let outcome =
        Worker::new(job, &config, "/bin/bash".into(), vec!["-c".into(), script.into()]).perform(client).await;
    assert_eq!(outcome.is_err(), case == "malformed");
    let reports = reports.lock().unwrap();
    assert!(!reports.is_empty());
    assert!(reports.iter().all(|r| !r.finished && r.execution.is_none()));
    if case == "pass" || case == "exit-error" {
        assert_eq!(reports.last().unwrap().score, 29);
        assert_eq!(
            reports.last().unwrap().survey_response.as_ref().unwrap().encode_to_vec(),
            b"\x0a\x02go\x12\x05human\x1a\x06manual\x22\x0fmodel-a,model-b"
        );
    }
    let result = completion.lock().unwrap().clone().unwrap();
    assert!(result.finished);
    assert_eq!(result.passed, case == "pass");
    assert_eq!(result.score, if case == "pass" { 29 } else { 0 });
    if case == "pass" || case == "exit-error" {
        assert_eq!(
            result.survey_response.as_ref().unwrap().encode_to_vec(),
            b"\x0a\x02go\x12\x05human\x1a\x06manual\x22\x0fmodel-a,model-b"
        );
    }
    let execution = result.execution.unwrap();
    if case == "pass" {
        assert_eq!(execution.exit_status, 0);
        assert_eq!(execution.stdout, "public-output\n");
        assert_eq!(execution.stderr, "private-output\n");
    }
    server_task.abort();
    std::fs::remove_dir_all(dir).unwrap();
}
