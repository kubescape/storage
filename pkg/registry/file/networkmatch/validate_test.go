package networkmatch

import "testing"

func TestValidateIPEntry(t *testing.T) {
	cases := []struct {
		name    string
		entry   string
		wantErr bool
	}{
		{"empty", "", true},
		{"literal-v4", "10.1.2.3", false},
		{"literal-v6", "2001:db8::1", false},
		{"cidr-v4", "10.0.0.0/8", false},
		{"cidr-v6", "2001:db8::/32", false},
		{"any-sentinel", "*", false},
		{"any-cidr-v4", "0.0.0.0/0", false},
		{"any-cidr-v6", "::/0", false},
		{"garbage", "not-an-ip", true},
		{"bad-cidr-mask", "10.0.0.0/40", true},
		{"bad-cidr-host", "not-an-ip/8", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateIPEntry(tc.entry)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateIPEntry(%q) err=%v, wantErr=%v", tc.entry, err, tc.wantErr)
			}
		})
	}
}

func TestValidateDNSEntry(t *testing.T) {
	cases := []struct {
		name    string
		entry   string
		wantErr bool
	}{
		{"empty", "", true},
		{"trailing-dot-only", ".", true},
		{"literal-with-dot", "api.stripe.com.", false},
		{"literal-no-dot", "api.stripe.com", false},
		{"leading-star", "*.example.com.", false},
		{"trailing-star", "internal.*", false},
		{"mid-ellipsis", "kubernetes.⋯.svc.cluster.local.", false},
		{"recursive-double-star", "**", true},
		{"recursive-in-middle", "foo.**.bar.", true},
		{"empty-inner-label", "foo..bar.", true},
		{"lone-star", "*", true},
		{"mid-star-rejected", "foo.*.bar.", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDNSEntry(tc.entry)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateDNSEntry(%q) err=%v, wantErr=%v", tc.entry, err, tc.wantErr)
			}
		})
	}
}
