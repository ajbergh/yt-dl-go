package main

import "testing"

func TestValidatePrivateDataDirACL(t *testing.T) {
	tests := []struct {
		name    string
		owner   string
		user    string
		dacl    bool
		allowed []string
		wantErr bool
	}{
		{name: "private user owned directory", owner: "S-1-5-21-user", user: "S-1-5-21-user", dacl: true},
		{name: "wrong owner", owner: "S-1-5-21-other", user: "S-1-5-21-user", dacl: true, wantErr: true},
		{name: "missing DACL", owner: "S-1-5-21-user", user: "S-1-5-21-user", wantErr: true},
		{name: "everyone grant", owner: "S-1-5-21-user", user: "S-1-5-21-user", dacl: true, allowed: []string{"S-1-1-0"}, wantErr: true},
		{name: "network grant", owner: "S-1-5-21-user", user: "S-1-5-21-user", dacl: true, allowed: []string{"S-1-5-2"}, wantErr: true},
		{name: "interactive grant", owner: "S-1-5-21-user", user: "S-1-5-21-user", dacl: true, allowed: []string{"S-1-5-4"}, wantErr: true},
		{name: "anonymous grant", owner: "S-1-5-21-user", user: "S-1-5-21-user", dacl: true, allowed: []string{"S-1-5-7"}, wantErr: true},
		{name: "authenticated users grant", owner: "S-1-5-21-user", user: "S-1-5-21-user", dacl: true, allowed: []string{"S-1-5-11"}, wantErr: true},
		{name: "remote interactive grant", owner: "S-1-5-21-user", user: "S-1-5-21-user", dacl: true, allowed: []string{"S-1-5-14"}, wantErr: true},
		{name: "builtin users grant", owner: "S-1-5-21-user", user: "S-1-5-21-user", dacl: true, allowed: []string{"S-1-5-32-545"}, wantErr: true},
		{name: "builtin guests grant", owner: "S-1-5-21-user", user: "S-1-5-21-user", dacl: true, allowed: []string{"S-1-5-32-546"}, wantErr: true},
		{name: "application packages grant", owner: "S-1-5-21-user", user: "S-1-5-21-user", dacl: true, allowed: []string{"S-1-15-2-1"}, wantErr: true},
		{name: "system and administrators remain allowed", owner: "S-1-5-21-user", user: "S-1-5-21-user", dacl: true, allowed: []string{"S-1-5-18", "S-1-5-32-544"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validatePrivateDataDirACL(test.owner, test.user, test.dacl, test.allowed)
			if (err != nil) != test.wantErr {
				t.Fatalf("validatePrivateDataDirACL() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}
