@echo off
cd ..

set GO111MODULE=on

if "%VERSION%"=="" set VERSION=dev
if "%COMMIT%"=="" set COMMIT=unknown
if "%BUILD_TIME%"=="" set BUILD_TIME=unknown

echo Building GoProxy version: %VERSION%
echo Commit: %COMMIT%
echo Build time: %BUILD_TIME%

go build -ldflags "-H=windowsgui -X main.Version=%VERSION% -X main.Commit=%COMMIT% -X main.BuildTime=%BUILD_TIME%"