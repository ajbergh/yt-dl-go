# RF-031: Separate-window dev mode adds trailing whitespace to environment values

Severity: low

Status: Fixed

Reviewed: 2026-11-09

Source: scripts/start-dev.ps1: SeparateWindows backend command

Evidence: cmd.exe uses unquoted set ADDR=127.0.0.1:port followed by space &&; that space becomes part of ADDR. Other set values in the same string have the same risk.

Resolution: Separate-window cmd.exe environment assignments quote the complete NAME=value pair. ADDR and other values no longer inherit trailing separator spaces; paths with spaces remain enclosed.

Validation: PowerShell parser validation passes. Executing the same quoted ADDR assignment reports exactly 127.0.0.1:8081 without trailing whitespace.
