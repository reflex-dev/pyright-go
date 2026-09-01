/*
 * shim-stringTokenUtils.ts
 *
 * Drop-in replacement for pyright-internal/src/parser/stringTokenUtils.ts that
 * delegates to the Go port via the tokenserver bridge.
 */

import type {
    FStringMiddleToken,
    StringToken,
} from '@pyright/parser/tokenizerTypes';
import { call } from './client';

export const enum UnescapeErrorType {
    InvalidEscapeSequence,
}

export interface UnescapeError {
    offset: number;
    length: number;
    errorType: UnescapeErrorType;
}

export interface UnescapedString {
    value: string;
    unescapeErrors: UnescapeError[];
    nonAsciiInBytes: boolean;
}

function toCodeUnits(text: string): number[] {
    const units: number[] = [];
    for (let i = 0; i < text.length; i++) {
        units.push(text.charCodeAt(i));
    }
    return units;
}

function fromCodeUnits(units: number[] | undefined): string {
    if (!units) {
        return '';
    }
    let result = '';
    const chunk = 4096;
    for (let i = 0; i < units.length; i += chunk) {
        result += String.fromCharCode(...units.slice(i, i + chunk));
    }
    return result;
}

export function getUnescapedString(
    stringToken: StringToken | FStringMiddleToken,
    elideCrlf = true
): UnescapedString {
    const raw = call({
        op: 'unescape',
        flags: stringToken.flags,
        escapedValue: toCodeUnits(stringToken.escapedValue),
        elideCrlf,
    });

    return {
        value: fromCodeUnits(raw.value),
        unescapeErrors: raw.unescapeErrors,
        nonAsciiInBytes: raw.nonAsciiInBytes,
    };
}
