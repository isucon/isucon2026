package contract_test

import (
	"encoding/base64"
	"testing"

	"google.golang.org/protobuf/proto"

	pbresources "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
	"github.com/isucon/isucon2026/portal/internal/model"
)

// CT-4: notifications.encoded_message と Web Push ペイロードは
// base64(Notification の protobuf バイナリ)。sw.ts が atob → decode する。
func TestNotificationEncodingContract(t *testing.T) {
	message := &pbresources.Notification{
		Content: &pbresources.Notification_ContentBenchmarkJob{
			ContentBenchmarkJob: &pbresources.Notification_BenchmarkJobMessage{BenchmarkJobId: 42},
		},
	}
	encoded, err := model.EncodeNotificationMessage(message)
	if err != nil {
		t.Fatal(err)
	}

	// 標準 base64（URL-safe ではない）であること: atob 互換
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("encoded_message must be standard base64: %v", err)
	}
	decoded := &pbresources.Notification{}
	if err := proto.Unmarshal(raw, decoded); err != nil {
		t.Fatalf("decoded payload must be a Notification: %v", err)
	}
	if decoded.GetContentBenchmarkJob().GetBenchmarkJobId() != 42 {
		t.Errorf("benchmark_job_id = %d, want 42", decoded.GetContentBenchmarkJob().GetBenchmarkJobId())
	}
}
