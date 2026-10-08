# ベンチマーカー連携（FR-B）と選手のジョブ操作（FR-C-2/3）の受け入れテスト。
# enqueue → 払い出し → 途中報告 → 確定報告 → best 更新 → 通知 の一巡を検証する。

RSpec.describe 'bench API' do
  let!(:now) { Time.now }
  let!(:team1) { create(:team) }
  let!(:instance1) { create(:contestant_instance, team: team1, number: 1) }

  before do
    @timing = { contest_start: now - 3600, contest_freeze: now + 3600, contest_end: now + 7200 }
  end

  def session_headers(extra = {})
    timing_headers({ 'X-Isux-Session-Team-Id' => team1.id.to_s }.merge(extra))
  end

  def enqueue_job!
    body = Isuxportal::Proto::Services::Contestant::EnqueueBenchmarkJobRequest.encode(
      Isuxportal::Proto::Services::Contestant::EnqueueBenchmarkJobRequest.new(target_id: instance1.id)
    )
    res = portal_post('/api/contestant/benchmark_jobs', {}, session_headers, body: body)
    expect(res.code).to eq('200')
    Isuxportal::Proto::Services::Contestant::EnqueueBenchmarkJobResponse.decode(res.body).job
  end

  def bench_receive(instance_name: team1.availability_zone, team_id: 0)
    body = Isuxportal::Proto::Services::Bench::ReceiveBenchmarkJobRequest.encode(
      Isuxportal::Proto::Services::Bench::ReceiveBenchmarkJobRequest.new(
        token: 'token', instance_name: instance_name, team_id: team_id,
      )
    )
    res = portal_post('/api/bench/receive', {}, timing_headers, body: body)
    expect(res.code).to eq('200')
    Isuxportal::Proto::Services::Bench::ReceiveBenchmarkJobResponse.decode(res.body)
  end

  def report(job_handle, marked_at:, score: -1, raw: 0, deduction: 0, finished: false, passed: false, execution: nil, expect_code: '200')
    result = Isuxportal::Proto::Resources::BenchmarkResult.new(
      finished: finished, passed: passed, score: score,
      score_breakdown: Isuxportal::Proto::Resources::BenchmarkResult::ScoreBreakdown.new(raw: raw, deduction: deduction),
      marked_at: Google::Protobuf::Timestamp.new(seconds: marked_at.to_i, nanos: marked_at.nsec),
      execution: execution,
    )
    body = Isuxportal::Proto::Services::Bench::ReportBenchmarkResultRequest.encode(
      Isuxportal::Proto::Services::Bench::ReportBenchmarkResultRequest.new(
        job_id: job_handle.job_id, handle: job_handle.handle, result: result,
      )
    )
    res = portal_post('/api/bench/benchmark_results', {}, timing_headers, body: body)
    expect(res.code).to eq(expect_code)
    res
  end

  def complete(job_handle, marked_at:, score: -1, raw: 0, deduction: 0, passed: true, exit_status: 0, expect_code: '200')
    result = Isuxportal::Proto::Resources::BenchmarkResult.new(
      finished: true, passed: passed, score: score,
      score_breakdown: Isuxportal::Proto::Resources::BenchmarkResult::ScoreBreakdown.new(raw: raw, deduction: deduction),
      marked_at: Google::Protobuf::Timestamp.new(seconds: marked_at.to_i, nanos: marked_at.nsec),
      execution: Isuxportal::Proto::Resources::BenchmarkResult::Execution.new(
        reason: passed ? 'ok' : 'ng', stdout: 'out', stderr: 'err', exit_status: exit_status,
      ),
    )
    body = Isuxportal::Proto::Services::Bench::CompleteBenchmarkJobRequest.encode(
      Isuxportal::Proto::Services::Bench::CompleteBenchmarkJobRequest.new(
        job_id: job_handle.job_id, handle: job_handle.handle, result: result,
      )
    )
    res = portal_post('/api/bench/benchmark_results/completion', {}, timing_headers, body: body)
    expect(res.code).to eq(expect_code)
    res
  end

  it 'runs enqueue → receive → progress → completion cycle with score sentinel (CT-7)' do
    job = enqueue_job!
    expect(job.status).to eq(:PENDING)
    expect(job.target_id).to eq(instance1.id)

    received = bench_receive
    handle = received.job_handle
    expect(handle).not_to be_nil
    expect(handle.job_id).to eq(job.id)
    expect(handle.handle.size).to be > 40
    expect(handle.target_ipv4_address).to eq(instance1.public_ipv4_address)
    expect(handle.all_ipv4_addresses.to_a).to eq([instance1.public_ipv4_address])
    expect(handle.all_ipv4_addresses[0]).to eq(instance1.public_ipv4_address)

    # 途中報告: score == -1 → raw - deduction で計算（CT-7）
    report(handle, marked_at: now, raw: 100, deduction: 10)
    result = BenchmarkResult.find_by!(benchmark_job_id: job.id)
    expect(result.score).to eq(90)
    expect(result.finished).to eq(false)

    # 古い marked_at の途中報告は黙って無視される（FR-B-3）
    report(handle, marked_at: now - 10, raw: 999, deduction: 0)
    expect(result.reload.score).to eq(90)

    # execution を含む途中報告は 400（FR-B-3）
    exec = Isuxportal::Proto::Resources::BenchmarkResult::Execution.new(exit_status: 0)
    report(handle, marked_at: now + 5, execution: exec, expect_code: '400')

    # 確定報告（FR-B-4）
    complete(handle, marked_at: now + 60, raw: 500, deduction: 50)
    job_row = BenchmarkJob.find(job.id)
    expect(job_row.status).to eq('finished')
    result.reload
    expect(result.score).to eq(450)
    expect(result.finished).to eq(true)
    expect(result.passed).to eq(true)

    # BenchmarkCompletionJob（同期実行）の副作用: best 更新 + メンバーへの通知
    expect(team1.reload.best_benchmark_result_id).to eq(result.id)
    leader = team1.members.first
    notifications = Notification.where(contestant_id: leader.id)
    expect(notifications.count).to eq(1)
    message = Isuxportal::Proto::Resources::Notification.decode(notifications.first.encoded_message.unpack1('m0'))
    expect(message.content_benchmark_job.benchmark_job_id).to eq(job.id)

    # ダッシュボード事前生成キャッシュ（CT-5）が焼かれ、audience dashboard が 304 経路で返る
    res = portal_get('/api/audience/dashboard', {}, timing_headers)
    expect(res.code).to eq('200')
    lb = Isuxportal::Proto::Services::Audience::DashboardResponse.decode(res.body).leaderboard
    expect(lb.teams.map { |t| t.team.id }).to include(team1.id)
    res2 = portal_get('/api/audience/dashboard', {}, timing_headers('If-None-Match' => res['etag']))
    expect(res2.code).to eq('304')
  end

  it 'rejects concurrent enqueue (DM-4)' do
    enqueue_job!
    body = Isuxportal::Proto::Services::Contestant::EnqueueBenchmarkJobRequest.encode(
      Isuxportal::Proto::Services::Contestant::EnqueueBenchmarkJobRequest.new(target_id: instance1.id)
    )
    res = portal_post('/api/contestant/benchmark_jobs', {}, session_headers, body: body)
    expect(res.code).to eq('422')
    err = Isuxportal::Proto::Error.decode(res.body)
    expect(err.human_descriptions.join).to include('concurrent job')
  end

  it 'rejects bad bench token with constant-time comparison result (FR-B-1)' do
    body = Isuxportal::Proto::Services::Bench::ReceiveBenchmarkJobRequest.encode(
      Isuxportal::Proto::Services::Bench::ReceiveBenchmarkJobRequest.new(token: 'wrong', instance_name: 'x')
    )
    res = portal_post('/api/bench/receive', {}, timing_headers, body: body)
    expect(res.code).to eq('400')
  end

  it 'returns empty job_handle when no pending jobs' do
    received = bench_receive
    expect(received.job_handle).to be_nil
  end

  it 'rejects report with wrong handle (FR-B-3)' do
    job = enqueue_job!
    received = bench_receive
    fake = Isuxportal::Proto::Services::Bench::ReceiveBenchmarkJobResponse::JobHandle.new(
      job_id: job.id, handle: 'wrong-handle',
    )
    report(fake, marked_at: now, expect_code: '404')
    _ = received
  end

  it 'recovers orphaned running jobs via cancel_owned (FR-B-2)' do
    job = enqueue_job!
    bench_receive
    expect(BenchmarkJob.find(job.id).status).to eq('running')

    body = Isuxportal::Proto::Services::Bench::CancelOwnedBenchmarkJobRequest.encode(
      Isuxportal::Proto::Services::Bench::CancelOwnedBenchmarkJobRequest.new(
        token: 'token', instance_name: team1.availability_zone,
      )
    )
    res = portal_post('/api/bench/cancel_owned', {}, timing_headers, body: body)
    expect(res.code).to eq('200')

    job_row = BenchmarkJob.find(job.id)
    expect(job_row.status).to eq('errored')
    result = BenchmarkResult.find_by!(benchmark_job_id: job.id)
    expect(result.reason).to include("Benchmarker didn't successfully finish")
    expect(result.passed).to eq(false)
  end

  it 'marks job errored when completion reports non-zero exit status (計画書 §11.8)' do
    job = enqueue_job!
    received = bench_receive
    complete(received.job_handle, marked_at: now + 30, raw: 100, deduction: 0, passed: false, exit_status: 1)
    expect(BenchmarkJob.find(job.id).status).to eq('errored')
    result = BenchmarkResult.find_by!(benchmark_job_id: job.id)
    expect(result.score).to eq(0) # finished && !passed → 0
  end

  it 'masks execution details for contestant but not admin (CT-6)' do
    job = enqueue_job!
    received = bench_receive
    complete(received.job_handle, marked_at: now + 30, raw: 100, deduction: 0)

    res = portal_get("/api/contestant/benchmark_jobs/#{job.id}", {}, session_headers)
    expect(res.code).to eq('200')
    contestant_view = Isuxportal::Proto::Services::Contestant::GetBenchmarkJobResponse.decode(res.body).job
    expect(contestant_view.result.execution.stderr).to eq('')
    expect(contestant_view.result.execution.exit_status).to eq(-2)
    expect(contestant_view.result.execution.exit_signal).to eq(-2)
    expect(contestant_view.instance_name).to eq('')

    res = portal_get("/api/admin/benchmark_jobs/#{job.id}", {}, timing_headers('X-Isux-Session-Staff' => '1'))
    expect(res.code).to eq('200')
    admin_view = Isuxportal::Proto::Services::Admin::GetBenchmarkJobResponse.decode(res.body).job
    expect(admin_view.result.execution.stderr).to eq('err')
    expect(admin_view.result.execution.exit_status).to eq(0)
    expect(admin_view.instance_name).to eq(team1.availability_zone)
  end
end
