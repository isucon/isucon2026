package httpapi

import (
	"mime/multipart"
	"net/http/httptest"
	"testing"
)

func TestRejectMultipartCredentialSource(t *testing.T) {
	tests := []struct {
		name string
		form *multipart.Form
		want bool
	}{
		{name: "post fields", form: &multipart.Form{Value: map[string][]string{"caption": {"today"}, "visibility": {"friends"}}}},
		{name: "credential value", form: &multipart.Form{Value: map[string][]string{"Access_Token": {"secret"}}}, want: true},
		{name: "credential file", form: &multipart.Form{File: map[string][]*multipart.FileHeader{"AUTHORIZATION": {{Filename: "secret"}}}}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest("POST", "/api/v1/posts", nil)
			request.MultipartForm = test.form
			err := rejectMultipartCredentialSource(request)
			if test.want && err == nil {
				t.Fatal("credential source accepted")
			}
			if !test.want && err != nil {
				t.Fatalf("valid multipart rejected: %v", err)
			}
		})
	}
}
