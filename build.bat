@echo off
REM ============================================================
REM SuperCli builder — kompiluje binarke
REM Wynik: supercli.exe w biezacym katalogu
REM ============================================================

setlocal

cd /d "%~dp0"

where go >nul 2>nul
if errorlevel 1 (
    echo [build.bat] BLAD: 'go' nie jest w PATH.
    echo             Zainstaluj Go 1.23+ z https://go.dev/dl/
    exit /b 1
)

echo [build.bat] Kompiluje supercli.exe...
go build -o supercli.exe .
if errorlevel 1 (
    echo [build.bat] BLAD: build nie powiodl sie.
    exit /b 1
)

if exist "supercli.exe" (
    echo [build.bat] OK: supercli.exe zbudowany.
    dir supercli.exe | findstr "supercli.exe"
) else (
    echo [build.bat] BLAD: binarka nie powstala mimo braku erroru.
    exit /b 1
)

endlocal
