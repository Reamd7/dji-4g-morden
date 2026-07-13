import React from 'react';

export interface SignalBarProps {
  /** RSSI 原始值 (AT+CSQ 量表:0-31,99=未知) */
  rssi: number;
  label?: string;
}

/**
 * 4G 信号强度条。把 AT+CSQ 的 0-31 RSSI 映射成 0-4 格,
 * 并标注 dBm(-113 + rssi*2)。99(未知)显示 0 格 + "无信号"。
 */
export const SignalBar: React.FC<SignalBarProps> = ({ rssi, label }) => {
  const unknown = rssi === 99 || rssi < 0;
  const bars = unknown ? 0 : Math.min(4, Math.ceil((rssi / 31) * 4));
  const dbm = unknown ? null : -113 + rssi * 2;
  const color = bars === 0 ? '#6b7280' : bars <= 1 ? '#dc2626' : bars <= 2 ? '#f59e0b' : '#22c55e';

  return (
    <div style={{ display: 'flex', alignItems: 'flex-end', gap: '3px' }}>
      {[1, 2, 3, 4].map((i) => (
        <div
          key={i}
          style={{
            width: '6px',
            height: `${i * 6}px`,
            background: i <= bars ? color : '#374151',
            borderRadius: '2px 2px 0 0',
          }}
        />
      ))}
      {label && (
        <span style={{ marginLeft: '8px', fontSize: '13px', color: '#9ca3af' }}>
          {label}
          {dbm !== null && ` · ${dbm} dBm`}
          {unknown && ' · 无信号'}
        </span>
      )}
    </div>
  );
};
