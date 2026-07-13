import React from 'react';

export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: 'primary' | 'secondary' | 'danger' | 'ghost';
  size?: 'small' | 'medium' | 'large';
}

const variantStyles: Record<NonNullable<ButtonProps['variant']>, React.CSSProperties> = {
  primary: { background: '#2563eb', color: '#fff' },
  secondary: { background: '#374151', color: '#fff' },
  danger: { background: '#dc2626', color: '#fff' },
  ghost: { background: 'transparent', color: '#93c5fd', border: '1px solid #93c5fd' },
};

const sizeStyles: Record<NonNullable<ButtonProps['size']>, React.CSSProperties> = {
  small: { padding: '4px 10px', fontSize: '12px' },
  medium: { padding: '8px 16px', fontSize: '14px' },
  large: { padding: '12px 24px', fontSize: '16px' },
};

export const Button: React.FC<ButtonProps> = ({
  variant = 'primary',
  size = 'medium',
  children,
  style,
  ...props
}) => (
  <button
    style={{
      border: 'none',
      borderRadius: '6px',
      cursor: 'pointer',
      fontWeight: 500,
      transition: 'opacity 0.15s',
      ...variantStyles[variant],
      ...sizeStyles[size],
      ...style,
    }}
    {...props}
  >
    {children}
  </button>
);
