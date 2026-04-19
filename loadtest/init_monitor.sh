#!/usr/bin/env bash

#kubectl -n devsy-pro set env deployment/devsy DEVSYDEBUG=true

kubectl -n devsy-pro port-forward "$(kubectl -n devsy-pro get pods -l app=devsy -o jsonpath="{.items[0].metadata.name}")" 8080:8080
