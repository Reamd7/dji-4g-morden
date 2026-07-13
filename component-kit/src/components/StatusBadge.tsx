import React from 'react';

export type StatusType = 'connected' | 'disconnected' | 'connecting' | 'error';

export interface StatusBadgeProps {
  status: StatusType;
  label?: string;
}

const statusConfig: Record<StatusType, { color: string; bg: string; text: string }> = {
  connected: { color: '#16a34a', bg: '#dcfce7', text: '已连接' },
  disconnected: { color: '#6b7280', bg: '#f3f4f6', text: '未连接' },
  connecting: { color: '#2563eb', bg: '#dbeafe', text: '连接中' },
  error: { color: '#dc2626', bg: '#fee2e2', text: '错误' },
};

export const StatusBadge: React.FC<StatusBadgeProps> = ({ status, label }) => {
  const cfg = statusConfig[status];
  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: '6px',
        padding: '4px 12px',
        borderRadius: '9999px',
        background: cfg.bg,
        color: cfg.color,
        fontSize: '13px',
        fontWeight: 500,
      }}
    >
      <span
        style={{
          width: '8px',
          height: '8px',
          borderRadius: '50%',
          background: cfg.color,
        }}
      />
      {label ?? cfg.text}
    </span>
  );
};
