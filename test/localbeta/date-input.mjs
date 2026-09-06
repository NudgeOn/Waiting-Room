// SPDX-License-Identifier: Apache-2.0
// HTML datetime-local normalizes a zero seconds component away. Playwright fill
// compares the resulting native value byte-for-byte, so supply that normal form.
export function nativeDateInput(value){
 if(!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}$/.test(value))throw Error('Fixture date must include seconds');
 return value.endsWith(':00')?value.slice(0,-3):value;
}
