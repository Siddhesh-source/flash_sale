import http from 'k6/http';
import { check, sleep } from 'k6';
import { uuidv4 } from 'https://jslib.k6.io/k6-utils/1.4.0/index.js';

// ─── Configuration ───────────────────────────────────────────────────────────
const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';

// Sale ID must be pre-seeded. Override via env: -e SALE_ID=<uuid>
const SALE_ID = __ENV.SALE_ID || 'replace-with-real-sale-uuid';

// ─── SLO Thresholds ──────────────────────────────────────────────────────────
export const options = {
  // Ramp: 0 → 50,000 VUs over 30s, hold 60s, ramp down 30s
  stages: [
    { duration: '30s', target: 50000 },
    { duration: '60s', target: 50000 },
    { duration: '30s', target: 0 },
  ],

  thresholds: {
    // SLO 2: p99 latency < 100ms
    'http_req_duration{scenario:default}': ['p(99)<100'],

    // SLO 1: error rate < 0.1%
    // k6 counts 4xx as non-failures by default; tag 5xx explicitly
    'http_req_failed': ['rate<0.001'],

    // Reserve-specific latency
    'http_req_duration{name:reserve}': ['p(99)<100', 'p(95)<50'],
  },

  // Write machine-readable summary
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)', 'p(99.9)', 'count'],
};

// ─── Setup: create a sale with large stock so the test doesn't hit sold-out ──
export function setup() {
  const res = http.post(
    `${BASE_URL}/api/sales`,
    JSON.stringify({
      name: `k6 Load Test ${new Date().toISOString()}`,
      total_stock: 100000000, // 100M — effectively unlimited for the test
      start_time: new Date(Date.now() - 60000).toISOString(),
      end_time: new Date(Date.now() + 3600000).toISOString(),
    }),
    { headers: { 'Content-Type': 'application/json' } }
  );

  check(res, { 'sale created': (r) => r.status === 201 });

  let saleID = SALE_ID;
  if (res.status === 201) {
    const body = JSON.parse(res.body);
    saleID = body.id;
  }

  return { saleID };
}

// ─── Main VU function ────────────────────────────────────────────────────────
export default function (data) {
  const saleID = data.saleID;
  const userID = uuidv4();
  const itemID = uuidv4();
  const idempotencyKey = uuidv4();

  const res = http.post(
    `${BASE_URL}/api/sales/${saleID}/reserve`,
    JSON.stringify({ user_id: userID, item_id: itemID }),
    {
      headers: {
        'Content-Type': 'application/json',
        'Idempotency-Key': idempotencyKey,
      },
      tags: { name: 'reserve' },
    }
  );

  // 201 = reserved, 409 = sold out — both are correct; 5xx is a failure
  check(res, {
    'reserve success or sold-out': (r) => r.status === 201 || r.status === 409,
    'no server error': (r) => r.status < 500,
  });

  // Minimal think time to avoid pure hammering; keeps rate realistic
  sleep(0.001);
}

// ─── Teardown ────────────────────────────────────────────────────────────────
export function handleSummary(data) {
  return {
    'k6/results.json': JSON.stringify(data, null, 2),
    stdout: textSummary(data, { indent: ' ', enableColors: true }),
  };
}

// Inline minimal textSummary (avoids external import issues in some k6 versions)
function textSummary(data, opts) {
  const { metrics } = data;
  const dur = metrics['http_req_duration'];
  if (!dur) return 'No duration metrics found.\n';
  const t = dur.values;
  return `
=== k6 Load Test Summary ===
  Requests:    ${metrics['http_reqs']?.values?.count ?? 'n/a'}
  Failed:      ${metrics['http_req_failed']?.values?.rate?.toFixed(4) ?? 'n/a'}
  p50 latency: ${t['med']?.toFixed(2) ?? 'n/a'} ms
  p95 latency: ${t['p(95)']?.toFixed(2) ?? 'n/a'} ms
  p99 latency: ${t['p(99)']?.toFixed(2) ?? 'n/a'} ms
  p99.9 lat:   ${t['p(99.9)']?.toFixed(2) ?? 'n/a'} ms
`;
}
