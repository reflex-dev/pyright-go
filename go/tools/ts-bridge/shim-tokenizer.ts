/*
 * shim-tokenizer.ts
 *
 * Drop-in replacement for pyright-internal/src/parser/tokenizer.ts that
 * delegates to the Go port via the tokenserver bridge. Aliasing this module in
 * place of the real one lets pyright's own tokenizer.test.ts run unmodified
 * against the Go implementation, so the tests do not have to be transliterated.
 *
 * Everything re-exported here other than the Tokenizer class comes from the
 * original TypeScript module; only the implementation under test is swapped.
 */

import { TextRange } from '@pyright/common/textRange';
import { TextRangeCollection } from '@pyright/common/textRangeCollection';
import {
    Comment,
    CommentType,
    OperatorFlags,
    OperatorType,
    Token,
    TokenType,
} from '@pyright/parser/tokenizerTypes';
import { call } from './client';

export interface IgnoreCommentRule {
    text: string;
    range: TextRange;
}

export interface IgnoreComment {
    range: TextRange;
    rulesList: IgnoreCommentRule[] | undefined;
}

export interface TokenizerOutput {
    tokens: TextRangeCollection<Token>;
    lines: TextRangeCollection<TextRange>;
    typeIgnoreLines: Map<number, IgnoreComment>;
    pyrightIgnoreLines: Map<number, IgnoreComment>;
    typeIgnoreAll: IgnoreComment | undefined;
    predominantEndOfLineSequence: string;
    hasPredominantTabSequence: boolean;
    predominantTabSequence: string;
    predominantSingleQuoteCharacter: string;
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
    // Chunked to stay clear of the argument-count limit on large strings.
    const chunk = 4096;
    for (let i = 0; i < units.length; i += chunk) {
        result += String.fromCharCode(...units.slice(i, i + chunk));
    }
    return result;
}

// The bridge sends doubles as raw IEEE bits so no formatting differences can
// creep into the comparison.
function fromBits(hex: string): number {
    const view = new DataView(new ArrayBuffer(8));
    for (let i = 0; i < 8; i++) {
        view.setUint8(i, parseInt(hex.substring(i * 2, i * 2 + 2), 16));
    }
    return view.getFloat64(0);
}

function reviveComments(raw: any[] | undefined): Comment[] | undefined {
    if (!raw) {
        return undefined;
    }
    return raw.map((c) => ({
        type: c.type as CommentType,
        start: c.start,
        length: c.length,
        value: fromCodeUnits(c.value),
    }));
}

function reviveToken(raw: any): Token {
    const base: any = {
        type: raw.type as TokenType,
        start: raw.start,
        length: raw.length,
    };

    const comments = reviveComments(raw.comments);
    if (comments !== undefined) {
        base.comments = comments;
    }

    if (raw.value !== undefined) base.value = raw.value;
    if (raw.keywordType !== undefined) base.keywordType = raw.keywordType;
    if (raw.operatorType !== undefined) base.operatorType = raw.operatorType;
    if (raw.newLineType !== undefined) base.newLineType = raw.newLineType;
    if (raw.flags !== undefined) base.flags = raw.flags;
    if (raw.escapedValue !== undefined) base.escapedValue = fromCodeUnits(raw.escapedValue);
    if (raw.prefixLength !== undefined) base.prefixLength = raw.prefixLength;
    if (raw.quoteMarkLength !== undefined) base.quoteMarkLength = raw.quoteMarkLength;
    if (raw.isInteger !== undefined) base.isInteger = raw.isInteger;
    if (raw.isImaginary !== undefined) base.isImaginary = raw.isImaginary;
    if (raw.numberValue !== undefined) {
        base.value = raw.numberValue.big !== undefined ? BigInt(raw.numberValue.big) : fromBits(raw.numberValue.num);
    }
    if (raw.indentAmount !== undefined) base.indentAmount = raw.indentAmount;
    if (raw.ambiguous !== undefined) {
        if (raw.type === TokenType.Indent) {
            base.isIndentAmbiguous = raw.ambiguous;
        } else {
            base.isDedentAmbiguous = raw.ambiguous;
        }
    }
    if (raw.matchesIndent !== undefined) base.matchesIndent = raw.matchesIndent;

    return base as Token;
}

function reviveIgnore(raw: any): IgnoreComment {
    return {
        range: { start: raw.range[0], length: raw.range[1] },
        rulesList: raw.rules
            ? raw.rules.map((r: any) => ({
                  text: fromCodeUnits(r.text),
                  range: { start: r.range[0], length: r.range[1] },
              }))
            : undefined,
    };
}

export class Tokenizer {
    tokenize(
        text: string,
        start?: number,
        length?: number,
        initialParenDepth = 0,
        useNotebookMode = false
    ): TokenizerOutput {
        // The range validation lives on the TypeScript side of the boundary so
        // that the thrown Error matches what the tests expect.
        if (start === undefined) {
            start = 0;
        } else if (start < 0 || start > text.length) {
            throw new Error(`Invalid range start (start=${start}, text.length=${text.length})`);
        }

        if (length === undefined) {
            length = text.length;
        } else if (length < 0 || start + length > text.length) {
            throw new Error(`Invalid range length (start=${start}, length=${length}, text.length=${text.length})`);
        }

        const raw = call({
            op: 'tokenize',
            text: toCodeUnits(text),
            start,
            length,
            initialParenDepth,
            useNotebookMode,
        });

        const typeIgnoreLines = new Map<number, IgnoreComment>();
        for (const [k, v] of Object.entries(raw.typeIgnoreLines ?? {})) {
            typeIgnoreLines.set(Number(k), reviveIgnore(v));
        }
        const pyrightIgnoreLines = new Map<number, IgnoreComment>();
        for (const [k, v] of Object.entries(raw.pyrightIgnoreLines ?? {})) {
            pyrightIgnoreLines.set(Number(k), reviveIgnore(v));
        }

        return {
            tokens: new TextRangeCollection<Token>(raw.tokens.map(reviveToken)),
            lines: new TextRangeCollection<TextRange>(
                raw.lines.map((l: number[]) => ({ start: l[0], length: l[1] }))
            ),
            typeIgnoreLines,
            pyrightIgnoreLines,
            typeIgnoreAll: raw.typeIgnoreAll ? reviveIgnore(raw.typeIgnoreAll) : undefined,
            predominantEndOfLineSequence: raw.eol,
            hasPredominantTabSequence: raw.hasTab,
            predominantTabSequence: raw.tab,
            predominantSingleQuoteCharacter: raw.quote,
        };
    }

    // The statics also go through the bridge, so what the tests observe is
    // what the Go port computes.
    static getOperatorInfo(operatorType: OperatorType): OperatorFlags {
        return call({ op: 'statics', which: 'getOperatorInfo', operatorType });
    }
    static isWhitespace(token: Token) {
        return call({ op: 'statics', which: 'isWhitespace', tokenType: token.type });
    }
    static isPythonKeyword(name: string, includeSoftKeywords = false): boolean {
        return call({ op: 'statics', which: 'isPythonKeyword', text: toCodeUnits(name), includeSoftKeywords });
    }
    static isPythonIdentifier(value: string) {
        return call({ op: 'statics', which: 'isPythonIdentifier', text: toCodeUnits(value) });
    }
    static isOperatorAssignment(operatorType?: OperatorType): boolean {
        if (operatorType === undefined) {
            return false;
        }
        return call({ op: 'statics', which: 'isOperatorAssignment', operatorType });
    }
    static isOperatorComparison(operatorType?: OperatorType): boolean {
        if (operatorType === undefined) {
            return false;
        }
        return call({ op: 'statics', which: 'isOperatorComparison', operatorType });
    }
}
