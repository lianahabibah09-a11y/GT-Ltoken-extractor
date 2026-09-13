@echo off
rem Build 3 binary terpisah dari 1 codebase (mode di-set via ldflags).
rem panen SID+bank   -> gt-harvest.exe   (sentuh Google, hasil: urls.txt + profiles)
rem replay token     -> gt-refresh.exe   (HTTP murni, hasil: tokens.txt)
rem installer SID    -> gt-login.exe     (login Google 1x per akun)
set GO="C:\Program Files\Go\bin\go.exe"
%GO% build -ldflags "-X main.defaultMode=harvest" -o gt-harvest.exe . || exit /b 1
%GO% build -ldflags "-X main.defaultMode=refresh" -o gt-refresh.exe . || exit /b 1
%GO% build -ldflags "-X main.defaultMode=login" -o gt-login.exe . || exit /b 1
echo BUILD_ALL_OK
