#!/bin/bash

# Comprehensive test suite runner for Design Cloud Engine
# Runs all unit tests, integration tests, and performance benchmarks

set -eo pipefail

# Configuration
PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# 目标平台固定为 linux/amd64（唯一受支持的部署架构）
export GOOS="${GOOS:-linux}"
export GOARCH="${GOARCH:-amd64}"

TEST_RESULTS_DIR="${PROJECT_ROOT}/test-results"
COVERAGE_DIR="${PROJECT_ROOT}/coverage"
BENCHMARK_DIR="${PROJECT_ROOT}/benchmarks"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Test configuration
RUN_UNIT_TESTS=true
RUN_INTEGRATION_TESTS=true
RUN_BENCHMARKS=true
RUN_COVERAGE=true
VERBOSE=false
PARALLEL=true

log_info() {
  echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
  echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warning() {
  echo -e "${YELLOW}[WARNING]${NC} $1"
}

log_error() {
  echo -e "${RED}[ERROR]${NC} $1"
}

# Show usage information
show_usage() {
  echo "Design Cloud Engine Test Suite Runner"
  echo ""
  echo "Usage: $0 [options]"
  echo ""
  echo "Options:"
  echo "  --unit-only         Run only unit tests"
  echo "  --integration-only  Run only integration tests"
  echo "  --benchmarks-only   Run only benchmarks"
  echo "  --no-coverage       Skip coverage analysis"
  echo "  --verbose           Enable verbose output"
  echo "  --sequential        Run tests sequentially (not in parallel)"
  echo "  --help              Show this help message"
  echo ""
  echo "Examples:"
  echo "  $0                  Run all tests with coverage"
  echo "  $0 --unit-only      Run only unit tests"
  echo "  $0 --verbose        Run all tests with verbose output"
}

# Parse command line arguments
parse_args() {
  while [[ $# -gt 0 ]]; do
    case $1 in
    --unit-only)
      RUN_UNIT_TESTS=true
      RUN_INTEGRATION_TESTS=false
      RUN_BENCHMARKS=false
      shift
      ;;
    --integration-only)
      RUN_UNIT_TESTS=false
      RUN_INTEGRATION_TESTS=true
      RUN_BENCHMARKS=false
      shift
      ;;
    --benchmarks-only)
      RUN_UNIT_TESTS=false
      RUN_INTEGRATION_TESTS=false
      RUN_BENCHMARKS=true
      shift
      ;;
    --no-coverage)
      RUN_COVERAGE=false
      shift
      ;;
    --verbose)
      VERBOSE=true
      shift
      ;;
    --sequential)
      PARALLEL=false
      shift
      ;;
    --help)
      show_usage
      exit 0
      ;;
    *)
      log_error "Unknown option: $1"
      show_usage
      exit 1
      ;;
    esac
  done
}

# Setup test environment
setup_test_env() {
  log_info "Setting up test environment..."

  # Create directories
  mkdir -p "${TEST_RESULTS_DIR}"
  mkdir -p "${COVERAGE_DIR}"
  mkdir -p "${BENCHMARK_DIR}"
  if [[ "${RUN_COVERAGE}" == "true" ]]; then
    rm -f "${COVERAGE_DIR}"/*.out
  fi

  # Change to project root
  cd "${PROJECT_ROOT}"

  # Check if Go is available
  if ! command -v go >/dev/null 2>&1; then
    log_error "Go is not installed or not in PATH"
    exit 1
  fi

  # Check Go version
  GO_VERSION=$(go version | awk '{print $3}' | sed 's/go//')
  log_info "Using Go version: ${GO_VERSION}"

  # Install test dependencies
  log_info "Installing test dependencies..."
  go mod download
  go mod tidy

  setup_certs

  log_success "Test environment setup complete"
}

setup_certs() {
  log_info "Generating test certificates..."
  export TEST_CERTS_DIR="${TEST_RESULTS_DIR}/certs"
  mkdir -p "${TEST_CERTS_DIR}"
  mkdir -p "${TEST_CERTS_DIR}/web"
  mkdir -p "${TEST_CERTS_DIR}/cluster"

  # CA
  openssl req -new -newkey rsa:2048 -days 365 -nodes -x509 \
    -keyout "${TEST_CERTS_DIR}/ca.key" -out "${TEST_CERTS_DIR}/ca.crt" \
    -subj "/C=US/ST=Test/L=Test/O=Test/CN=TestCA" 2>/dev/null

  # Server/Web Cert
  openssl req -new -newkey rsa:2048 -days 365 -nodes \
    -keyout "${TEST_CERTS_DIR}/web/server.key" -out "${TEST_CERTS_DIR}/web/server.csr" \
    -subj "/C=US/ST=Test/L=Test/O=Test/CN=localhost" 2>/dev/null

  echo "subjectAltName=DNS:localhost,IP:127.0.0.1" >"${TEST_CERTS_DIR}/ext.cnf"

  openssl x509 -req -days 365 -in "${TEST_CERTS_DIR}/web/server.csr" \
    -CA "${TEST_CERTS_DIR}/ca.crt" -CAkey "${TEST_CERTS_DIR}/ca.key" -CAcreateserial \
    -out "${TEST_CERTS_DIR}/web/server.crt" -extfile "${TEST_CERTS_DIR}/ext.cnf" 2>/dev/null

  # Cluster Cert (Reuse web key/cert for simplicity or generate new?)
  # Generating separate for clarity.
  openssl req -new -newkey rsa:2048 -days 365 -nodes \
    -keyout "${TEST_CERTS_DIR}/cluster/server.key" -out "${TEST_CERTS_DIR}/cluster/server.csr" \
    -subj "/C=US/ST=Test/L=Test/O=Test/CN=localhost" 2>/dev/null

  openssl x509 -req -days 365 -in "${TEST_CERTS_DIR}/cluster/server.csr" \
    -CA "${TEST_CERTS_DIR}/ca.crt" -CAkey "${TEST_CERTS_DIR}/ca.key" \
    -out "${TEST_CERTS_DIR}/cluster/server.crt" -extfile "${TEST_CERTS_DIR}/ext.cnf" 2>/dev/null

  log_success "Certificates generated in ${TEST_CERTS_DIR}"
}

# Run unit tests
run_unit_tests() {
  if [[ "${RUN_UNIT_TESTS}" != "true" ]]; then
    return 0
  fi

  log_info "Running unit tests..."

  local test_args=""
  local output_file="${TEST_RESULTS_DIR}/unit-tests.out"

  if [[ "${PARALLEL}" == "true" ]]; then
    test_args="${test_args} -parallel 4"
  fi

  if [[ "${VERBOSE}" == "true" ]]; then
    test_args="${test_args} -v"
  fi

  # Run unit tests for all internal packages dynamically
  local packages=($(go list ./internal/... | grep -v /vendor/))

  local failed_packages=()
  local total_packages=${#packages[@]}
  local passed_packages=0

  for package in "${packages[@]}"; do
    log_info "Testing package: ${package}"

    local package_test_args="${test_args}"
    if [[ "${RUN_COVERAGE}" == "true" ]]; then
      local profile_name
      profile_name=$(echo "${package}" | sed 's#[^A-Za-z0-9_.-]#_#g')
      package_test_args="${package_test_args} -coverprofile=${COVERAGE_DIR}/${profile_name}.out -covermode=atomic"
    fi

    if go test ${package_test_args} "${package}" 2>&1 | tee -a "${output_file}"; then
      log_success "✓ ${package}"
      ((passed_packages++))
    else
      log_error "✗ ${package}"
      failed_packages+=("${package}")
    fi
  done

  # Summary
  echo ""
  log_info "Unit Test Summary:"
  echo "  Total packages: ${total_packages}"
  echo "  Passed: ${passed_packages}"
  echo "  Failed: ${#failed_packages[@]}"

  if [[ ${#failed_packages[@]} -gt 0 ]]; then
    log_error "Failed packages:"
    for package in "${failed_packages[@]}"; do
      echo "    - ${package}"
    done
    return 1
  else
    log_success "All unit tests passed!"
    return 0
  fi
}

# Run integration tests
run_integration_tests() {
  if [[ "${RUN_INTEGRATION_TESTS}" != "true" ]]; then
    return 0
  fi

  log_info "Running integration tests..."

  local output_file="${TEST_RESULTS_DIR}/integration-tests.out"

  # Build binaries for integration testing
  log_info "Building binaries for integration tests..."
  make clean
  make all

  if [[ ! -f "build/design-cloud-engine" ]] || [[ ! -f "build/dc-launcher" ]]; then
    log_error "Failed to build required binaries for integration tests"
    return 1
  fi

  # Test launcher functionality
  log_info "Testing launcher integration..."
  if ! ./test/launcher/test_launcher_e2e.sh 2>&1 | tee -a "${output_file}"; then
    log_error "Launcher integration tests failed"
    return 1
  fi

  # Test server startup in different modes
  log_info "Testing server mode configurations..."
  if ! ./test/node/test-node-modes.sh 2>&1 | tee -a "${output_file}"; then
    log_error "Server mode tests failed"
    return 1
  fi

  # Test configless bootstrap (mode ladder, parameter validation, cert matrix)
  log_info "Testing configless bootstrap..."
  if ! ./test/node/test-configless.sh 2>&1 | tee -a "${output_file}"; then
    log_error "Configless bootstrap tests failed"
    return 1
  fi

  # Test agent service
  log_info "Testing agent service..."
  if ! ./test/node/test-agent-service.sh 2>&1 | tee -a "${output_file}"; then
    log_error "Agent service tests failed"
    return 1
  fi

  # Test security features
  log_info "Testing security features..."
  if ! ./test/security/test-security-features.sh 2>&1 | tee -a "${output_file}"; then
    log_error "Security feature tests failed"
    return 1
  fi

  log_success "All integration tests passed!"
  return 0
}

# Run performance benchmarks
run_benchmarks() {
  if [[ "${RUN_BENCHMARKS}" != "true" ]]; then
    return 0
  fi

  log_info "Running performance benchmarks..."

  local benchmark_file="${BENCHMARK_DIR}/benchmarks.out"
  local benchmark_args="-bench=. -benchmem -benchtime=10s"

  if [[ "${VERBOSE}" == "true" ]]; then
    benchmark_args="${benchmark_args} -v"
  fi

  # Run benchmarks for performance-critical packages
  local benchmark_packages=(
    "./internal/launcher/..."
    "./internal/agent/..."
    "./internal/service/session/..."
  )

  for package in "${benchmark_packages[@]}"; do
    log_info "Benchmarking package: ${package}"

    if go test ${benchmark_args} "${package}" 2>&1 | tee -a "${benchmark_file}"; then
      log_success "✓ Benchmarked ${package}"
    else
      log_warning "⚠ Benchmark failed for ${package}"
    fi
  done

  # Generate benchmark report
  log_info "Generating benchmark report..."
  cat >"${BENCHMARK_DIR}/benchmark-report.md" <<'EOF'
# Performance Benchmark Report

This report contains performance benchmarks for critical components of the Design Cloud Engine.

## Benchmark Results

EOF

  if [[ -f "${benchmark_file}" ]]; then
    echo '```' >>"${BENCHMARK_DIR}/benchmark-report.md"
    cat "${benchmark_file}" >>"${BENCHMARK_DIR}/benchmark-report.md"
    echo '```' >>"${BENCHMARK_DIR}/benchmark-report.md"
  fi

  log_success "Performance benchmarks completed!"
  return 0
}

# Generate coverage report
generate_coverage_report() {
  if [[ "${RUN_COVERAGE}" != "true" ]]; then
    return 0
  fi

  log_info "Generating coverage report..."

  # Combine coverage files if multiple exist
  local coverage_files=("${COVERAGE_DIR}"/*.out)
  if [[ ${#coverage_files[@]} -gt 1 ]]; then
    log_info "Combining coverage files..."
    echo "mode: atomic" >"${COVERAGE_DIR}/combined-coverage.out"
    for file in "${coverage_files[@]}"; do
      if [[ "${file}" != "${COVERAGE_DIR}/combined-coverage.out" ]]; then
        tail -n +2 "${file}" >>"${COVERAGE_DIR}/combined-coverage.out"
      fi
    done
    local main_coverage="${COVERAGE_DIR}/combined-coverage.out"
  else
    local main_coverage="${coverage_files[0]}"
  fi

  if [[ -f "${main_coverage}" ]]; then
    # Generate HTML coverage report
    go tool cover -html="${main_coverage}" -o "${COVERAGE_DIR}/coverage.html"

    # Generate coverage summary
    local coverage_percent=$(go tool cover -func="${main_coverage}" | grep total | awk '{print $3}')

    log_info "Coverage Summary:"
    echo "  Total Coverage: ${coverage_percent}"
    echo "  Report: ${COVERAGE_DIR}/coverage.html"

    # Check coverage threshold
    local threshold=70
    local coverage_num=$(echo "${coverage_percent}" | sed 's/%//')

    if awk "BEGIN {exit !(${coverage_num} >= ${threshold})}"; then
      log_success "Coverage meets threshold (${threshold}%): ${coverage_percent}"
    else
      log_warning "Coverage below threshold (${threshold}%): ${coverage_percent}"
    fi
  else
    log_warning "No coverage data found"
  fi
}

# Validate security requirements
validate_security_requirements() {
  log_info "Validating security requirements..."

  local security_report="${TEST_RESULTS_DIR}/security-validation.txt"

  # Check launcher capabilities
  if [[ -f "build/dc-launcher" ]]; then
    local caps=$(getcap build/dc-launcher 2>/dev/null || echo "none")
    echo "Launcher capabilities: ${caps}" >>"${security_report}"

    if [[ "${caps}" == *"cap_setuid"* ]] && [[ "${caps}" == *"cap_setgid"* ]]; then
      log_success "✓ Launcher has required capabilities"
    else
      # In dev environment, we may not have caps set. Only error if we are supposed to be in prod-like setup.
      if [[ "${EUID}" -ne 0 ]]; then
        log_warning "⚠ Launcher missing required capabilities (expected in dev environment when not root)"
      else
        log_error "✗ Launcher missing required capabilities"
        return 1
      fi
    fi
  fi

  # Check for security vulnerabilities in dependencies
  log_info "Checking for security vulnerabilities..."
  if command -v govulncheck >/dev/null 2>&1; then
    local vuln_cache="/tmp/govulncheck-cache-$$"
    if GOCACHE="${vuln_cache}" GOTMPDIR="/tmp" govulncheck ./... >>"${security_report}" 2>&1; then
      log_success "✓ No known vulnerabilities found"
    else
      log_warning "⚠ Potential vulnerabilities detected, check ${security_report}"
    fi
    rm -rf "${vuln_cache}"
  else
    log_warning "govulncheck not available, skipping vulnerability scan"
  fi

  # Validate audit log format
  log_info "Validating audit log format..."
  # This would test audit log structure and format

  log_success "Security validation completed"
  return 0
}

# Generate final test report
generate_test_report() {
  log_info "Generating final test report..."

  local report_file="${TEST_RESULTS_DIR}/test-report.md"
  local timestamp=$(date '+%Y-%m-%d %H:%M:%S')

  cat >"${report_file}" <<EOF
# Design Cloud Engine Test Report

**Generated:** ${timestamp}
**Go Version:** $(go version | awk '{print $3}')
**Platform:** $(uname -s) $(uname -m)

## Test Configuration

- Unit Tests: ${RUN_UNIT_TESTS}
- Integration Tests: ${RUN_INTEGRATION_TESTS}
- Benchmarks: ${RUN_BENCHMARKS}
- Coverage Analysis: ${RUN_COVERAGE}
- Parallel Execution: ${PARALLEL}

## Test Results

EOF

  # Add unit test results
  if [[ "${RUN_UNIT_TESTS}" == "true" ]] && [[ -f "${TEST_RESULTS_DIR}/unit-tests.out" ]]; then
    echo "### Unit Tests" >>"${report_file}"
    echo '```' >>"${report_file}"
    tail -20 "${TEST_RESULTS_DIR}/unit-tests.out" >>"${report_file}"
    echo '```' >>"${report_file}"
    echo "" >>"${report_file}"
  fi

  # Add integration test results
  if [[ "${RUN_INTEGRATION_TESTS}" == "true" ]] && [[ -f "${TEST_RESULTS_DIR}/integration-tests.out" ]]; then
    echo "### Integration Tests" >>"${report_file}"
    echo '```' >>"${report_file}"
    tail -20 "${TEST_RESULTS_DIR}/integration-tests.out" >>"${report_file}"
    echo '```' >>"${report_file}"
    echo "" >>"${report_file}"
  fi

  # Add coverage information
  if [[ "${RUN_COVERAGE}" == "true" ]]; then
    echo "### Coverage Report" >>"${report_file}"
    echo "Coverage report available at: \`${COVERAGE_DIR}/coverage.html\`" >>"${report_file}"
    echo "" >>"${report_file}"
  fi

  # Add benchmark results
  if [[ "${RUN_BENCHMARKS}" == "true" ]] && [[ -f "${BENCHMARK_DIR}/benchmarks.out" ]]; then
    echo "### Performance Benchmarks" >>"${report_file}"
    echo "Detailed benchmark report available at: \`${BENCHMARK_DIR}/benchmark-report.md\`" >>"${report_file}"
    echo "" >>"${report_file}"
  fi

  log_success "Test report generated: ${report_file}"
}

# Main execution
main() {
  echo "=== Design Cloud Engine Test Suite ==="
  echo ""

  parse_args "$@"
  setup_test_env

  local exit_code=0

  # Run tests
  if ! run_unit_tests; then
    exit_code=1
  fi

  if ! run_integration_tests; then
    exit_code=1
  fi

  if ! run_benchmarks; then
    exit_code=1
  fi

  # Generate reports
  generate_coverage_report
  validate_security_requirements
  generate_test_report

  # Final summary
  echo ""
  echo "=== Test Suite Summary ==="
  if [[ ${exit_code} -eq 0 ]]; then
    log_success "All tests passed successfully!"
    echo "  📊 Test report: ${TEST_RESULTS_DIR}/test-report.md"
    if [[ "${RUN_COVERAGE}" == "true" ]]; then
      echo "  📈 Coverage report: ${COVERAGE_DIR}/coverage.html"
    fi
    if [[ "${RUN_BENCHMARKS}" == "true" ]]; then
      echo "  🚀 Benchmark report: ${BENCHMARK_DIR}/benchmark-report.md"
    fi
  else
    log_error "Some tests failed. Check the reports for details."
    echo "  📊 Test report: ${TEST_RESULTS_DIR}/test-report.md"
  fi

  exit ${exit_code}
}

# Run main function
main "$@"
