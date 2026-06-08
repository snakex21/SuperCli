@echo off
REM ============================================================
REM SuperCli runner — click and go
REM Uzywa SUPERCLI_HOME=.  zeby wszystko zostalo w biezacym
REM katalogu (sandbox mode = portable by default).
REM ============================================================

setlocal

REM Zmien katalog na ten, w ktorym lezy run.bat.
cd /d "%~dp0"

REM Jesli nie ma zbudowanej binarki, zbuduj.
if not exist "supercli.exe" (
    echo [run.bat] supercli.exe not found, building...
    call "%~dp0build.bat"
    if errorlevel 1 (
        echo [run.bat] build failed.
        exit /b 1
    )
)

REM Sandbox mode: home = biezacy katalog.
set "SUPERCLI_HOME=."
".\supercli.exe" %*

endlocal
