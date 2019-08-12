# Changelog
All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.7.0] - 2019-08-13

## Changed
- **The used port has changed from `9288` to `9629`** to align with [the port allocation politics of Prometheus integrations](https://github.com/prometheus/prometheus/wiki/Default-port-allocations).

## [0.6.0] - 2019-07-17

>**! Warning:** This release significantly changes logic of creating Gitlab issues and labeling scheme. 
Please read more about the new grouping feature.  

## Changed
- Dynamic labels are now added as scoped labels to the issues in form `label::value`
- To every issue the group- To every issue the grouping labels are added as scoped labels same way as dynamic labels. 
ing labels are added as scoped labels same way as dynamic labels. 

## Added
- If alert comes and opened issue with the same group labels is present in the Gitlab, 
the rendered template is just appended to this already existing issue instead of creating a new one.
This applies only for issues younger than by default `1h` which can be controlled by new flag `--group.interval`. 
Every appended issue gets new scoped label `appended-alerts::<numer>` with number of times it was appended.
- Readme notes about contributing and release.

## [0.5.0] - 2019-07-10

## Added
- Added dynamic label addition from the alert labels using flag `dynamic.issue.label.name`

## [0.4.1] - 2019-06-27

## Fixed
- Metric `app_build_info` is now initialized to value `1`

## [0.4.0] - 2019-06-27

## Added
- Added time to log messages
- Added metric `app_build_info` with info about version of the app, build etc.

## [0.3.0] - 2019-06-26

## Changed
- Removed Gitlab call from readiness probe since the alerts
are just enqueued and retrying should take care of that.

## Added
- Check on startup that Gitlab is reachable.

## [0.2.0] - 2019-06-26

## Added:
- Added `status_code` to metrics and access log.

## Changed
- Refactored HTTP server middleware.

## [0.1.0] - 2019-06-25

Initial release

