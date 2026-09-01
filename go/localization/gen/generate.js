/*
 * generate.js
 *
 * One-shot code generator that transliterates the accessor surface of
 * pyright-internal/src/localization/localize.ts (v1.1.412) into Go.
 *
 * localize.ts declares ~1500 message accessors, each of which is either
 *
 *     export const name = () => getRawString('Namespace.name');
 *
 * or, when the message has substitution parameters,
 *
 *     export const name = () =>
 *         new ParameterizedString<{ a: string; b: number }>(getRawString('Namespace.name'));
 *
 * Transcribing those by hand would be both enormous and error-prone, so this
 * script parses the declarations out of localize.ts and emits the equivalent
 * Go accessors. The message text itself is not duplicated into Go source at
 * all: the package embeds the same package.nls.*.json files the TypeScript
 * build ships and looks keys up at runtime.
 *
 * Usage:
 *   node generate.js <path-to-src/localization> <output.go>
 */

'use strict';

const fs = require('fs');
const path = require('path');

const localizationDir = process.argv[2];
const outputPath = process.argv[3];

if (!localizationDir || !outputPath) {
    console.error('usage: node generate.js <src/localization dir> <output.go>');
    process.exit(1);
}

const source = fs.readFileSync(path.join(localizationDir, 'localize.ts'), 'utf8');

// ---------------------------------------------------------------------------
// Parse localize.ts
// ---------------------------------------------------------------------------

// Collapse the file to a single logical line per `export const` declaration so
// that prettier's line wrapping doesn't matter.
const declRegex =
    /export const ([A-Za-z0-9_]+) = \(\) =>\s*(?:new ParameterizedString<\{([^}]*)\}>\(\s*)?getRawString\('([^']+)'\)/g;

// Namespace boundaries, so each declaration can be attributed to its namespace.
const namespaceRanges = [];
{
    const nsRegex = /export namespace ([A-Za-z0-9_]+) \{/g;
    let m;
    while ((m = nsRegex.exec(source)) !== null) {
        // Skip the outer `Localizer` namespace; only the inner ones carry messages.
        if (m[1] === 'Localizer') {
            continue;
        }
        namespaceRanges.push({ name: m[1], start: m.index });
    }
    for (let i = 0; i < namespaceRanges.length; i++) {
        namespaceRanges[i].end = i + 1 < namespaceRanges.length ? namespaceRanges[i + 1].start : source.length;
    }
}

function namespaceAt(index) {
    for (const ns of namespaceRanges) {
        if (index >= ns.start && index < ns.end) {
            return ns.name;
        }
    }
    return undefined;
}

const entriesByNamespace = new Map();
let match;
while ((match = declRegex.exec(source)) !== null) {
    const [, name, paramBlock, key] = match;
    const ns = namespaceAt(match.index);
    if (!ns) {
        throw new Error(`could not attribute declaration ${name} to a namespace`);
    }

    const expectedKey = `${ns}.${name}`;
    if (key !== expectedKey) {
        throw new Error(`key mismatch: declaration ${ns}.${name} looks up '${key}'`);
    }

    let params = [];
    if (paramBlock !== undefined) {
        params = paramBlock
            .split(';')
            .map((p) => p.trim())
            .filter((p) => p.length > 0)
            .map((p) => {
                const parts = p.split(':').map((s) => s.trim());
                if (parts.length !== 2) {
                    throw new Error(`unparsable parameter '${p}' in ${key}`);
                }
                if (parts[1] !== 'string' && parts[1] !== 'number') {
                    throw new Error(`unexpected parameter type '${parts[1]}' in ${key}`);
                }
                return { name: parts[0], type: parts[1] };
            });
    }

    if (!entriesByNamespace.has(ns)) {
        entriesByNamespace.set(ns, []);
    }
    entriesByNamespace.get(ns).push({ name, key, params });
}

// ---------------------------------------------------------------------------
// Cross-check against the default (en-us) string table
// ---------------------------------------------------------------------------

const enUs = JSON.parse(fs.readFileSync(path.join(localizationDir, 'package.nls.en-us.json'), 'utf8'));
let missing = 0;
for (const [ns, entries] of entriesByNamespace) {
    for (const entry of entries) {
        const value = enUs[ns] && enUs[ns][entry.name];
        if (value === undefined) {
            console.error(`WARNING: no en-us string for ${entry.key}`);
            missing++;
        }
    }
}
if (missing > 0) {
    console.error(`${missing} accessors have no default string`);
}

// ---------------------------------------------------------------------------
// Emit Go
// ---------------------------------------------------------------------------

const goKeywords = new Set([
    'break', 'case', 'chan', 'const', 'continue', 'default', 'defer', 'else', 'fallthrough',
    'for', 'func', 'go', 'goto', 'if', 'import', 'interface', 'map', 'package', 'range',
    'return', 'select', 'struct', 'switch', 'type', 'var',
]);

function exported(name) {
    return name.charAt(0).toUpperCase() + name.slice(1);
}

function goParamName(name) {
    // `type` and friends are Go keywords; the TypeScript sources use several.
    return goKeywords.has(name) ? name + 'Param' : name;
}

// A stable, collision-free Go type name for each parameterized message.
function psTypeName(ns, name) {
    return `PS${ns}${exported(name)}`;
}

const namespaceGoNames = {
    Diagnostic: 'LocMessage',
    DiagnosticAddendum: 'LocAddendum',
    CodeAction: 'LocCodeAction',
    Completion: 'LocCompletion',
    Rename: 'LocRename',
    Service: 'LocService',
};

const out = [];
out.push('// Code generated by localization/gen/generate.js from');
out.push('// pyright-internal/src/localization/localize.ts (pyright 1.1.412). DO NOT EDIT.');
out.push('');
out.push('package localization');
out.push('');
out.push('import "strconv"');
out.push('');
out.push('var _ = strconv.Itoa');
out.push('');

let totalPlain = 0;
let totalParameterized = 0;

for (const [ns, entries] of entriesByNamespace) {
    const goName = namespaceGoNames[ns];
    if (!goName) {
        throw new Error(`no Go name mapped for namespace ${ns}`);
    }
    const structName = `${goName.charAt(0).toLowerCase()}${goName.slice(1)}NS`;

    out.push(`// ${structName} carries the accessors of Localizer.${ns}.`);
    out.push(`type ${structName} struct{}`);
    out.push('');
    out.push(`// ${goName} corresponds to Localizer.${ns}.`);
    out.push(`var ${goName} = ${structName}{}`);
    out.push('');

    for (const entry of entries) {
        const method = exported(entry.name);

        if (entry.params.length === 0) {
            totalPlain++;
            out.push(`// ${method} corresponds to Localizer.${ns}.${entry.name}.`);
            out.push(`func (${structName}) ${method}() string {`);
            out.push(`\treturn getRawString(${JSON.stringify(entry.key)})`);
            out.push('}');
            out.push('');
            continue;
        }

        totalParameterized++;
        const typeName = psTypeName(ns, entry.name);

        out.push(`// ${typeName} is the ParameterizedString for Localizer.${ns}.${entry.name}.`);
        out.push(`type ${typeName} struct{ formatString string }`);
        out.push('');
        out.push(`// GetFormatString returns the raw, unsubstituted message.`);
        out.push(`func (p ${typeName}) GetFormatString() string { return p.formatString }`);
        out.push('');

        const sig = entry.params
            .map((p) => `${goParamName(p.name)} ${p.type === 'number' ? 'int' : 'string'}`)
            .join(', ');
        out.push(`// Format substitutes the message parameters.`);
        out.push(`func (p ${typeName}) Format(${sig}) string {`);
        out.push('\tstr := p.formatString');
        for (const p of entry.params) {
            const value =
                p.type === 'number' ? `strconv.Itoa(${goParamName(p.name)})` : goParamName(p.name);
            out.push(`\tstr = replaceAll(str, ${JSON.stringify('{' + p.name + '}')}, ${value})`);
        }
        out.push('\treturn str');
        out.push('}');
        out.push('');

        out.push(`// ${method} corresponds to Localizer.${ns}.${entry.name}.`);
        out.push(`func (${structName}) ${method}() ${typeName} {`);
        out.push(`\treturn ${typeName}{formatString: getRawString(${JSON.stringify(entry.key)})}`);
        out.push('}');
        out.push('');
    }
}

// A key list, so a test can assert that every accessor resolves against the
// default string table.
out.push('// allMessageKeys lists every key the accessors above look up.');
out.push('var allMessageKeys = []string{');
for (const [, entries] of entriesByNamespace) {
    for (const entry of entries) {
        out.push(`\t${JSON.stringify(entry.key)},`);
    }
}
out.push('}');
out.push('');
out.push('// forEachMessage resolves every message and passes it to fn.');
out.push('func forEachMessage(fn func(message string)) {');
out.push('\tfor _, key := range allMessageKeys {');
out.push('\t\tfn(getRawString(key))');
out.push('\t}');
out.push('}');
out.push('');

fs.writeFileSync(outputPath, out.join('\n'));
console.log(
    `generated ${outputPath}: ${entriesByNamespace.size} namespaces, ` +
        `${totalPlain} plain + ${totalParameterized} parameterized accessors`
);
