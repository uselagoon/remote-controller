package messenger

import (
	"encoding/base64"
	"testing"
)

func Test_checkRestoreVersionFromCore(t *testing.T) {
	type args struct {
		resource string
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "test1 - raw backup.appuio.ch/v1alpha1 that would be provided by core",
			args: args{
				resource: "eyJhcGlWZXJzaW9uIjoiYmFja3VwLmFwcHVpby5jaC92MWFscGhhMSIsImtpbmQiOiJSZXN0b3JlIiwibWV0YWRhdGEiOnsibmFtZSI6InJlc3RvcmUtYmYwNzJhMC11cXhxbzMifSwic3BlYyI6eyJzbmFwc2hvdCI6ImJmMDcyYTA5ZTE3NzI2ZGE1NGFkYzc5OTM2ZWM4NzQ1NTIxOTkzNTk5ZDQxMjExZGZjOTQ2NmRmZDViYzMyYTUiLCJyZXN0b3JlTWV0aG9kIjp7InMzIjp7fX0sImJhY2tlbmQiOnsiczMiOnsiYnVja2V0IjoiYmFhcy1uZ2lueC1leGFtcGxlIn0sInJlcG9QYXNzd29yZFNlY3JldFJlZiI6eyJrZXkiOiJyZXBvLXB3IiwibmFtZSI6ImJhYXMtcmVwby1wdyJ9fX19",
			},
			want: "backup.appuio.ch/v1alpha1",
		},
		{
			name: "test2 - just metadata and spec, no kind or apiversion",
			args: args{
				resource: "eyJtZXRhZGF0YSI6eyJuYW1lIjoicmVzdG9yZS1iZjA3MmEwLXVxeHFvMyJ9LCJzcGVjIjp7InNuYXBzaG90IjoiYmYwNzJhMDllMTc3MjZkYTU0YWRjNzk5MzZlYzg3NDU1MjE5OTM1OTlkNDEyMTFkZmM5NDY2ZGZkNWJjMzJhNSIsInJlc3RvcmVNZXRob2QiOnsiczMiOnt9fSwiYmFja2VuZCI6eyJzMyI6eyJidWNrZXQiOiJiYWFzLW5naW54LWV4YW1wbGUifSwicmVwb1Bhc3N3b3JkU2VjcmV0UmVmIjp7ImtleSI6InJlcG8tcHciLCJuYW1lIjoiYmFhcy1yZXBvLXB3In19fX0=",
			},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, _ := base64.StdEncoding.DecodeString(tt.args.resource)
			got, err := checkRestoreVersionFromCore(raw)
			if (err != nil) != tt.wantErr {
				t.Errorf("checkRestoreVersionFromCore() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("checkRestoreVersionFromCore() = %v, want %v", got, tt.want)
			}
		})
	}
}
