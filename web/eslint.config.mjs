import antfu from '@antfu/eslint-config'

// 一个文件匹配到多个设置了 no-restricted-imports 的配置对象时，只有最后一个生效。所以 features 的配置要再带上 featureInternals。core 和 shared 的配置禁止引用 features 下的任何路径，已经包含了 featureInternals。
const featureInternals = {
  pattern: /^(?:@\/|\.\.?\/(?:.*\/)?)features\/[^/]+\/[^/]/i,
  message: '功能之间只能通过对方的 index.ts 引用。',
}

const appImports = {
  pattern: /^(?:@\/|\.\.\/(?:.*\/)?)app(?:\/|$)/i,
  message: 'features、core 和 shared 不能依赖 app。',
}

// no-restricted-imports 不检查 import()，动态导入由 no-restricted-syntax 用同一个正则检查。
// no-restricted-syntax 同样只有最后一个配置对象生效，所以要带上 @antfu/eslint-config 设置的两项。升级 antfu 时，这两项要和它保持一致。
function restrictImports(...restrictions) {
  return {
    'no-restricted-imports': ['error', { patterns: restrictions.map(({ pattern, message }) => ({ regex: pattern.source, message })) }],
    'no-restricted-syntax': ['error', 'TSEnumDeclaration[const=true]', 'TSExportAssignment', ...restrictions.map(({ pattern, message }) => ({ selector: `ImportExpression[source.value=${pattern}]`, message }))],
  }
}

export default antfu(
  {
    react: true,
    // lint:fix 会改写生成物，之后 protocolgen -check 就会失败。
    ignores: ['src/**/*.gen.ts'],
  },
  {
    files: ['src/**/*.{ts,tsx}'],
    rules: restrictImports(featureInternals),
  },
  {
    files: ['src/features/**/*.{ts,tsx}'],
    rules: restrictImports(featureInternals, {
      pattern: /^\.\.\//,
      message: '功能内部使用相对路径，功能之外的模块用 @/ 引用。',
    }, appImports),
  },
  {
    files: ['src/core/**/*.{ts,tsx}'],
    rules: restrictImports({
      pattern: /^(?:@\/|\.\.\/(?:.*\/)?)features(?:\/|$)/i,
      message: 'core 不能依赖 features。',
    }, appImports),
  },
  {
    files: ['src/shared/**/*.{ts,tsx}'],
    rules: restrictImports({
      pattern: /^(?:@\/|\.\.\/(?:.*\/)?)(?:core|features)(?:\/|$)/i,
      message: 'shared 不能依赖 core 和 features。',
    }, appImports),
  },
  {
    // @antfu/eslint-config 对声明文件关闭了 no-restricted-syntax，上面的配置对象会把它重新打开。
    files: ['**/*.d.?([cm])ts'],
    rules: { 'no-restricted-syntax': 'off' },
  },
)
