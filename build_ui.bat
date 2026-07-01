@echo off
REM ============================================================
REM SuperCli UI builder - kompiluje web GUI / app-mode UI
REM Wynik: supercli-web.exe w katalogu repo
REM ============================================================

setlocal

cd /d "%~dp0"

where go >nul 2>nul
if errorlevel 1 (
    echo [build_ui.bat] BLAD: 'go' nie jest w PATH.
    echo                Zainstaluj Go 1.23+ z https://go.dev/dl/
    pause
    exit /b 1
)

if not exist "cmd\supercli-web\main.go" (
    echo [build_ui.bat] BLAD: nie znaleziono cmd\supercli-web\main.go.
    echo                Najpierw upewnij sie, ze web GUI jest w repo.
    pause
    exit /b 1
)

echo [build_ui.bat] Kompiluje supercli-web.exe bez okna konsoli...
go build -ldflags="-H=windowsgui" -o supercli-web.exe ./cmd/supercli-web
if errorlevel 1 (
    echo [build_ui.bat] BLAD: build UI nie powiodl sie.
    pause
    exit /b 1
)

if not exist "supercli-web.exe" (
    echo [build_ui.bat] BLAD: binarka UI nie powstala mimo braku erroru.
    pause
    exit /b 1
)

echo BUILD UI OK
echo   %~dp0supercli-web.exe

if exist "%~dp0..\supercli-web.exe" (
    copy /y "supercli-web.exe" "%~dp0..\supercli-web.exe" >nul
    if errorlevel 1 (
        echo [build_ui.bat] UWAGA: nie udalo sie skopiowac do katalogu nadrzednego.
        pause
        exit /b 1
    )
    echo   %~dp0..\supercli-web.exe ^(zaktualizowany^)
)

echo.
echo Uruchomienie:
echo   .\supercli-web.exe
echo   .\supercli-web.exe --echo
echo   .\supercli-web.exe --no-window --addr 127.0.0.1:8765

endlocal
exit /b 0
