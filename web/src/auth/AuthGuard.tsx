import type { ReactNode } from 'react'
import { Navigate, useLocation } from 'react-router-dom'
import { useAuth } from './AuthContext'

// AuthGuard wraps the main app. Decision tree:
//
//   loading → render a quiet placeholder
//   setup required → force /setup
//   not authenticated → force /login
//   authenticated → render children
//
// /login and /setup render outside of the guard (they're routed above it),
// so they never get bounced by their own redirects.
export default function AuthGuard({ children }: { children: ReactNode }) {
  const { status, loading } = useAuth()
  const location = useLocation()

  if (loading) {
    return (
      <div className="min-h-screen flex items-center justify-center text-slate-500 dark:text-zinc-500 text-sm">
        Loading…
      </div>
    )
  }

  if (status?.setupRequired && location.pathname !== '/setup') {
    return <Navigate to="/setup" replace />
  }
  if (!status?.authenticated && !status?.setupRequired && location.pathname !== '/login') {
    return <Navigate to="/login" replace />
  }

  return <>{children}</>
}
