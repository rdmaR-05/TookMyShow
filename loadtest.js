import http from 'k6/http';
import { check } from 'k6';

export const options = {
  scenarios: {
    thundering_herd: {
      executor: 'shared-iterations',
      vus: 50,        
      iterations: 50, 
      maxDuration: '10s',
    },
  },
};

export default function () {
  const url = 'http://localhost:30000/events/44444444-4444-4444-4444-444444444444/book';
  
  const payload = JSON.stringify({
    seatID: '66666666-6666-6666-6666-666666666662',
  });

  const params = {
    headers: {
      'Content-Type': 'application/json',
      'Authorization': 'Bearer ' + 'eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJlbWFpbCI6InJodXR2aWpyc2QxMEBnbWFpbC5jb20iLCJleHAiOjE3NzUwNTY2MjR9.BX4Vu_39q-NS5Ejge6WS0J3LoqIXxqF2XYUbl0sk-9s', 
    },
  };

  const res = http.post(url, payload, params);

  if (__VU === 1 && __ITER === 0) {
    console.log(`\n🚨 SERVER SAID: ${res.status}`);
    console.log(`🚨 RESPONSE BODY: ${res.body}\n`);
  }
  check(res, {
    'is status 200 (Got the lock)': (r) => r.status === 200,
    'is status 409 (Redis blocked them)': (r) => r.status === 409,
    'no server crashes (Not 500)': (r) => r.status !== 500,
  });
}