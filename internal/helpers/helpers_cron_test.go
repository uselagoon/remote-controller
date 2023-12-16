package helpers

import (
	"testing"
)

func TestConvertCrontab(t *testing.T) {
	type args struct {
		namespace string
		cron      string
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "test1",
			args: args{
				namespace: "example-com-main",
				cron:      "M * * * *",
			},
			want: "31 * * * *",
		},
		{
			name: "test2",
			args: args{
				namespace: "example-com-main",
				cron:      "M/5 * * * *",
			},
			want: "1,6,11,16,21,26,31,36,41,46,51,56 * * * *",
		},
		{
			name: "test3",
			args: args{
				namespace: "example-com-main",
				cron:      "M H(2-4) * * *",
			},
			want: "31 3 * * *",
		},
		{
			name: "test4",
			args: args{
				namespace: "example-com-main",
				cron:      "M H(22-2) * * *",
			},
			want: "31 1 * * *",
		},
		{
			name: "test5",
			args: args{
				namespace: "example-com-main",
				cron:      "M/15 H(22-2) * * *",
			},
			want: "1,16,31,46 1 * * *",
		},
		{
			name: "test6 - invalid minutes definition",
			args: args{
				namespace: "example-com-main",
				cron:      "M/H5 H(22-2) * * *",
			},
			wantErr: true,
		},
		{
			name: "test7 - invalid hour definiton",
			args: args{
				namespace: "example-com-main",
				cron:      "M/15 H(H2-2) * * *",
			},
			wantErr: true,
		},
		{
			name: "test8",
			args: args{
				namespace: "example-com-main",
				cron:      "M/15 H(22-2) 3,5 * *",
			},
			want: "1,16,31,46 1 3,5 * *",
		},
		{
			name: "test9",
			args: args{
				namespace: "example-com-main",
				cron:      "M/15 H(22-2) * 10-12 *",
			},
			want: "1,16,31,46 1 * 10-12 *",
		},
		{
			name: "test10 - invalid dayofweek range",
			args: args{
				namespace: "example-com-main",
				cron:      "M/15 H(22-2) * * 1-8",
			},
			wantErr: true,
		},
		{
			name: "test11",
			args: args{
				namespace: "example-com-main",
				cron:      "15 * * * 1,2,3,6",
			},
			want: "15 * * * 1,2,3,6",
		},
		{
			name: "test12",
			args: args{
				namespace: "example-com-main",
				cron:      "15 * 1-31 * *",
			},
			want: "15 * 1-31 * *",
		},
		{
			name: "test13 - invalid day range",
			args: args{
				namespace: "example-com-main",
				cron:      "15 * 1-32 * *",
			},
			wantErr: true,
		},
		{
			name: "test14 - set hours",
			args: args{
				namespace: "example-com-main",
				cron:      "M/15 23 * * 0-5",
			},
			want: "1,16,31,46 23 * * 0-5",
		},
		{
			name: "test15 - set day",
			args: args{
				namespace: "example-com-main",
				cron:      "M/15 * 31 * 0-5",
			},
			want: "1,16,31,46 * 31 * 0-5",
		},
		{
			name: "test16 - set month",
			args: args{
				namespace: "example-com-main",
				cron:      "M/15 * * 11 0-5",
			},
			want: "1,16,31,46 * * 11 0-5",
		},
		{
			name: "test17 - pick day of week between range",
			args: args{
				namespace: "example-com-main",
				cron:      "M/15 * * * D(0-4)",
			},
			want: "1,16,31,46 * * * 3",
		},
		{
			name: "test18 - pick day of week between range",
			args: args{
				namespace: "example-com-main",
				cron:      "M/15 * * * D(2-6)",
			},
			want: "1,16,31,46 * * * 5",
		},
		{
			name: "test19 - pick day of week random",
			args: args{
				namespace: "example-com-main",
				cron:      "M/15 * * * D",
			},
			want: "1,16,31,46 * * * 1",
		},
		{
			name: "test20 - pick day of month between range",
			args: args{
				namespace: "example-com-main",
				cron:      "M/15 * D(1-31) * *",
			},
			want: "1,16,31,46 * 2 * *",
		},
		{
			name: "test21 - pick day of month between range",
			args: args{
				namespace: "example-com-main",
				cron:      "M/15 * D(15-25) * *",
			},
			want: "1,16,31,46 * 16 * *",
		},
		{
			name: "test22 - pick day of month random",
			args: args{
				namespace: "example-com-main",
				cron:      "M/15 * D * *",
			},
			want: "1,16,31,46 * 27 * *",
		},
		{
			name: "test23 - pick day of month between range with invalid end day (only support 1-31)",
			args: args{
				namespace: "example-com-main",
				cron:      "M/15 * D(1-32) * *",
			},
			wantErr: true,
		},
		{
			name: "test24 - pick day of week between range with invalid end day (only support 0-6)",
			args: args{
				namespace: "example-com-main",
				cron:      "M/15 * * * D(0-7)",
			},
			wantErr: true,
		},
		{
			name: "test26 - pick day of month between range with invalid start day (only support 1-31)",
			args: args{
				namespace: "example-com-main",
				cron:      "M/15 * D(0-28) * *",
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ConvertCrontab(tt.args.namespace, tt.args.cron)
			if err != nil {
				if !tt.wantErr {
					t.Errorf("ConvertCrontab() error = %v, wantErr %v", err, tt.wantErr)
				}
			}
			if got != tt.want {
				if !tt.wantErr {
					t.Errorf("ConvertCrontab() = %v, want %v", got, tt.want)
				} else {
					t.Errorf("ConvertCrontab() = %v, wantErr %v", got, tt.wantErr)
				}
			}
		})
	}
}
