[CmdletBinding()]
param(
    [Parameter(ValueFromRemainingArguments = $true)]
    $RemainingArgs
)

& "$PSScriptRoot\start-dev.ps1" @RemainingArgs

