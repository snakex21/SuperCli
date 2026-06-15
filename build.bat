@echo off
REM ============================================================
REM SuperCli builder - kompiluje binarke
REM Wynik: supercli.exe w katalogu repo (i w nadrzednym, jesli tam tez jest)
REM ============================================================

setlocal

cd /d "%~dp0"

where go >nul 2>nul
if errorlevel 1 (
    echo [build.bat] BLAD: 'go' nie jest w PATH.
    echo             Zainstaluj Go 1.23+ z https://go.dev/dl/
    pause
    exit /b 1
)

echo [build.bat] Kompiluje supercli.exe...
go build -o supercli.exe ./cmd/supercli
if errorlevel 1 (
    echo [build.bat] BLAD: build nie powiodl sie.
    pause
    exit /b 1
)

if not exist "supercli.exe" (
    echo [build.bat] BLAD: binarka nie powstala mimo braku erroru.
    pause
    exit /b 1
)

echo BUILD OK
echo   %~dp0supercli.exe

if exist "%~dp0..\supercli.exe" (
    copy /y "supercli.exe" "%~dp0..\supercli.exe" >nul
    if errorlevel 1 (
        echo [build.bat] UWAGA: nie udalo sie skopiowac do katalogu nadrzednego.
        pause
        exit /b 1
    )
    echo   %~dp0..\supercli.exe ^(zaktualizowany^)
)

endlocal
exit /b 0
