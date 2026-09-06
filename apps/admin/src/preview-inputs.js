// SPDX-License-Identifier: Apache-2.0
// Field feedback only. The Go planner remains authoritative for every request.
export const initialPlan = {
  schemaVersion: 1,
  profile: 'standard-10k',
  regionId: 'ap-northeast-2',
  queuePolicy: 'fifo',
  expectedPeakVisitors: 10000,
  limits: {
    maxActiveAdmissionLeases: 1000,
    admissionsPerMinute: 600,
    admissionTtlSeconds: 900,
  },
  totp: {mode: 'configurable', enabled: true},
};

export const planSteps = ['규모 선택', '운영 환경', '유량 설정', '관리자 보안', '계획 확인', '비용 비교'];

const slugPattern = /^[a-z][a-z0-9-]{0,62}$/;
const pricePattern = /^(0|[1-9][0-9]{0,8})(\.[0-9]{1,6})?$/;
const currencyPattern = /^[A-Z]{3}$/;

// JavaScript's $ may match before a final newline; Go's validator rejects it.
function matchesEntirely(pattern, value) {
  return typeof value === 'string' && pattern.exec(value)?.[0] === value;
}

function integerError(value, min, max) {
  return Number.isInteger(value) && value >= min && value <= max
    ? ''
    : `${min.toLocaleString('en-US')}~${max.toLocaleString('en-US')} 사이의 정수를 입력하세요.`;
}

function checkInteger(errors, key, value, min, max) {
  const error = integerError(value, min, max);
  if (error) errors[key] = error;
}

export function validatePlanStep(draft, step) {
  const input = draft ?? {};
  const errors = {};
  if (step === 0) {
    if (!['standard-10k', 'high-scale-100k'].includes(input.profile)) {
      errors.profile = 'Standard 10K 또는 High Scale 100K를 선택하세요.';
    }
  } else if (step === 1) {
    if (!matchesEntirely(slugPattern, input.regionId)) {
      errors.regionId = '영문 소문자로 시작하는 1~63자의 소문자·숫자·하이픈을 입력하세요.';
    }
  } else if (step === 2) {
    const high = input.profile === 'high-scale-100k';
    const cap = high ? 100000 : 10000;
    const limits = input.limits ?? {};
    checkInteger(errors, 'expectedPeakVisitors', input.expectedPeakVisitors, 1, cap);
    checkInteger(errors, 'maxActiveAdmissionLeases', limits.maxActiveAdmissionLeases, 1, cap);
    checkInteger(errors, 'admissionsPerMinute', limits.admissionsPerMinute, 1, high ? 60000 : 6000);
    checkInteger(errors, 'admissionTtlSeconds', limits.admissionTtlSeconds, 60, 3600);
  } else if (step === 3) {
    const totp = input.totp ?? {};
    if (!['configurable', 'forced_on'].includes(totp.mode)) {
      errors.totpMode = '운영자 선택 또는 TOTP 강제 ON 정책을 선택하세요.';
    }
    if (typeof totp.enabled !== 'boolean') {
      errors.totpEnabled = 'TOTP 사용 여부를 선택하세요.';
    } else if (totp.mode === 'forced_on' && !totp.enabled) {
      errors.totpEnabled = '강제 ON 정책에서는 TOTP를 켜야 합니다.';
    }
  }
  return errors;
}

function validDate(value) {
  if (typeof value !== 'string') return false;
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
  if (!match || match[0] !== value) return false;
  const [, year, month, day] = match.map(Number);
  if (year < 2000 || year > 9999 || month < 1 || month > 12) return false;
  const leap = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
  const days = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  return day >= 1 && day <= days[month - 1];
}

// regionId is already validated in the plan and is supplied by the caller.
export function validatePrice(price) {
  const input = price ?? {};
  const errors = {};
  if (input.schemaVersion !== 1) errors.schemaVersion = '지원하는 비용 입력 형식은 버전 1입니다.';
  if (!matchesEntirely(slugPattern, input.provider)) {
    errors.provider = '영문 소문자로 시작하는 1~63자의 소문자·숫자·하이픈을 입력하세요.';
  }
  if (!matchesEntirely(currencyPattern, input.currency)) {
    errors.currency = 'USD처럼 영문 대문자 3자리 통화 코드를 입력하세요.';
  }
  if (!validDate(input.asOf)) {
    errors.asOf = '2000-01-01~9999-12-31 사이의 유효한 기준일을 입력하세요.';
  }
  checkInteger(errors, 'fractionDigits', input.fractionDigits, 0, 4);
  checkInteger(errors, 'monthlyHours', input.monthlyHours, 1, 744);
  checkInteger(errors, 'highWorkerVolumeGiB', input.highWorkerVolumeGiB, 1, 65536);
  checkInteger(errors, 'expectedEgressGiB', input.expectedEgressGiB, 0, 1000000000);
  for (const key of ['standardHostHourly', 'highWorkerHourly', 'volumeGiBMonthly']) {
    if (!matchesEntirely(pricePattern, input[key])) {
      errors[key] = '0~999999999.999999의 단가를 소수점 이하 최대 6자리로 입력하세요. 쉼표·음수·지수 표기는 사용할 수 없습니다.';
    }
  }
  return errors;
}
