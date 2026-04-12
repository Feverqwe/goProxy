@echo off
cd ..

set GO111MODULE=on

if "%VERSION%"=="" set VERSION=dev

echo Building GoProxy version: %VERSION%

go build -ldflags "-H=windowsgui -X main.Version=$VERSION"