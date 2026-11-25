# Benchmarks

`make bench` runs every benchmark six times and writes the results to `bench.txt`, or to the file `BENCH_OUT` names. Each benchmark reads one small fixture of `testdata` and one generated tree of the size of a large module, about 90 `.tf` files and 500 KB. `lint` is also timed with the test profile of `testdata/profile`, and on the generated tree with a generated schema handoff of about 22 MB of JSON, the size of a large provider at two versions. Compare two runs with benchstat:

```sh
make bench BENCH_OUT=/tmp/old.txt
# change the code
make bench BENCH_OUT=/tmp/new.txt
go run golang.org/x/perf/cmd/benchstat@v0.0.0-20261117185222-4febdb082b2c /tmp/old.txt /tmp/new.txt
```

On a pull request the `bench` job of the `ci` workflow runs the benchmarks on the head and on the base and prints the comparison in the job summary, with a line for a run that failed or a base it could not run. Shared runners are noisy: read the comparison as a hint, and confirm a change on one machine. The job never fails the build.
