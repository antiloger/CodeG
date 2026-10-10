import {
  Box,
  Braces,
  CircleHelp,
  GitBranch,
  Hash,
  Layers,
  Package,
  Parentheses,
  Rows3,
  SquareFunction,
  Tag,
  Variable,
  type LucideIcon,
} from "lucide-react"

const ICONS: Record<string, LucideIcon> = {
  package: Package,
  module: Layers,
  func: SquareFunction,
  function: SquareFunction,
  method: SquareFunction,
  callee: SquareFunction,
  closure: Parentheses,
  type: Box,
  alias: Box,
  type_param: Braces,
  const: Hash,
  var: Variable,
  global: Variable,
  local: Variable,
  param: Variable,
  result: Variable,
  field: Rows3,
  label: Tag,
  candidate: GitBranch,
  unknown: CircleHelp,
}

export function KindIcon({ kind, className }: { kind: string; className?: string }) {
  const Icon = ICONS[kind] ?? Box
  return <Icon className={className} aria-hidden />
}
