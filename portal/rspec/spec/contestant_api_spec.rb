# 選手向け API（FR-C）の受け入れテスト: clarification / 通知 / Push 購読 / CFn。

RSpec.describe 'contestant API' do
  let!(:now) { Time.now }
  let!(:team1) { create(:team) }
  let!(:team2) { create(:team) }
  let!(:leader) { team1.members.first }

  before do
    @timing = { contest_start: now - 3600, contest_freeze: now + 3600, contest_end: now + 7200 }
  end

  def session_headers(extra = {})
    timing_headers({ 'X-Isux-Session-Contestant-Id' => leader.id.to_s }.merge(extra))
  end

  describe 'clarifications (FR-C-4)' do
    it 'posts a question and lists own + disclosed ones' do
      req = Isuxportal::Proto::Services::Contestant::RequestClarificationRequest.new(question: 'how?')
      res = portal_post('/api/contestant/clarifications', {}, session_headers,
                        body: Isuxportal::Proto::Services::Contestant::RequestClarificationRequest.encode(req))
      expect(res.code).to eq('200')
      clar = Isuxportal::Proto::Services::Contestant::RequestClarificationResponse.decode(res.body).clarification
      expect(clar.question).to eq('how?')
      expect(clar.team_id).to eq(team1.id)

      # 他チームの非公開 clar は見えない / 公開 clar は見える
      Clarification.create!(team: team2, question: 'secret', original_question: 'secret', admin: false)
      Clarification.create!(team: team2, question: 'open q', original_question: 'open q', admin: false,
                            answered_at: now, answer: 'open a', disclosed: true)

      res = portal_get('/api/contestant/clarifications', {}, session_headers)
      body = Isuxportal::Proto::Services::Contestant::ListClarificationsResponse.decode(res.body)
      questions = body.clarifications.map(&:question)
      expect(questions).to include('how?', 'open q')
      expect(questions).not_to include('secret')

      own = body.clarifications.find { |c| c.question == 'how?' }
      expect(own.original_question).to eq('how?') # 自チーム分は original が見える
      other = body.clarifications.find { |c| c.question == 'open q' }
      expect(other.original_question).to eq('')
    end
  end

  describe 'notifications (FR-C-5)' do
    it 'lists unread by default, marks them read, and honors after param' do
      clar = Clarification.create!(team: team1, question: 'q', original_question: 'q', admin: false)
      # 運営の回答（同期ジョブが通知を作る）
      req = Isuxportal::Proto::Services::Admin::RespondClarificationRequest.new(id: clar.id, disclose: false, answer: 'a')
      portal_put("/api/admin/clarifications/#{clar.id}", timing_headers('X-Isux-Session-Staff' => '1'),
                 body: Isuxportal::Proto::Services::Admin::RespondClarificationRequest.encode(req))
      expect(Notification.where(contestant_id: leader.id).count).to eq(1)

      res = portal_get('/api/contestant/notifications', {}, session_headers)
      body = Isuxportal::Proto::Services::Contestant::ListNotificationsResponse.decode(res.body)
      expect(body.notifications.size).to eq(1)
      expect(body.notifications[0].content_clarification.clarification_id).to eq(clar.id)
      expect(body.notifications[0].content_clarification.owned).to eq(true)
      expect(body.last_answered_clarification_id).to eq(clar.id)
      first_id = body.notifications[0].id

      # 既読化されたので次は空
      res = portal_get('/api/contestant/notifications', {}, session_headers)
      body = Isuxportal::Proto::Services::Contestant::ListNotificationsResponse.decode(res.body)
      expect(body.notifications.size).to eq(0)

      # after 指定なら既読でも id > after を返す
      res = portal_get('/api/contestant/notifications', { 'after' => (first_id - 1).to_s }, session_headers)
      body = Isuxportal::Proto::Services::Contestant::ListNotificationsResponse.decode(res.body)
      expect(body.notifications.map(&:id)).to eq([first_id])
    end

    it 'fires a test notification via POST (FR-C-5)' do
      res = portal_post('/api/contestant/notifications', {}, session_headers)
      expect(res.code).to eq('204')
      n = Notification.where(contestant_id: leader.id).first
      message = Isuxportal::Proto::Resources::Notification.decode(n.encoded_message.unpack1('m0'))
      expect(message.content_test).not_to be_nil
    end
  end

  describe 'push subscriptions (FR-C-6)' do
    it 'subscribes and unsubscribes' do
      req = Isuxportal::Proto::Services::Contestant::SubscribeNotificationRequest.new(
        endpoint: 'https://push.example.com/sub/1', p256dh: 'key', auth: 'auth',
      )
      res = portal_post('/api/contestant/push_subscriptions', {}, session_headers,
                        body: Isuxportal::Proto::Services::Contestant::SubscribeNotificationRequest.encode(req))
      expect(res.code).to eq('200')
      expect(PushSubscription.where(contestant_id: leader.id).count).to eq(1)

      unreq = Isuxportal::Proto::Services::Contestant::UnsubscribeNotificationRequest.new(
        endpoint: 'https://push.example.com/sub/1',
      )
      res = portal_delete('/api/contestant/push_subscriptions', session_headers,
                          body: Isuxportal::Proto::Services::Contestant::UnsubscribeNotificationRequest.encode(unreq))
      expect(res.code).to eq('200')
      expect(PushSubscription.where(contestant_id: leader.id).count).to eq(0)
    end

    it 'rejects invalid endpoint with 422' do
      req = Isuxportal::Proto::Services::Contestant::SubscribeNotificationRequest.new(
        endpoint: 'not a url', p256dh: 'key', auth: 'auth',
      )
      res = portal_post('/api/contestant/push_subscriptions', {}, session_headers,
                        body: Isuxportal::Proto::Services::Contestant::SubscribeNotificationRequest.encode(req))
      expect(res.code).to eq('422')
    end
  end

  describe 'cloud formation (FR-C-7)' do
    it 'returns contest template' do
      res = portal_get('/api/contestant/cloud_formation', {}, session_headers)
      expect(res.code).to eq('200')
      body = Isuxportal::Proto::Services::Contestant::GetCloudFormationResponse.decode(res.body)
      expect(body.template).to include('AWSTemplateFormatVersion')
      expect(body.template).to include('ami-y')
    end
  end

  describe 'guards' do
    it 'redirects to login when the team is disqualified (current_contestant が nil になる)' do
      team1.update!(disqualified: true)
      res = portal_get('/api/contestant/dashboard', {}, session_headers)
      expect(res.code).to eq('302')
      expect(res['location']).to start_with('/session/new/github?')
    end
  end
end
