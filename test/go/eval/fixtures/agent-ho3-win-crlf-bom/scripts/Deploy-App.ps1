<#
.SYNOPSIS
  Wdraża aplikację do Azure. Uruchamiać w Windows PowerShell 5.1.
#>
param(
    [string]$Environment = 'prod'
)

$ErrorActionPreference = 'Stop'
$Region = 'westeurope'
$ResourceGroup = "rg-aplikacja-$Environment"

Write-Host "Rozpoczynam wdrażanie do regionu $Region (grupa: $ResourceGroup)…"
az group create --name $ResourceGroup --location $Region | Out-Null
az deployment group create --resource-group $ResourceGroup --template-file "$PSScriptRoot\main.bicep" | Out-Null
Write-Host "Wdrażanie zakończone pomyślnie. Żółć i gęś są bezpieczne."
