module.exports = {
  content: [
    "./templates/**/*.html",
    "./demo.html",
  ],
  safelist: [
    'bg-blue-200',
    'bg-blue-100',
    'bg-orange-100',
    'bg-orange-200',
    'bg-red-200',
    'bg-gray-100',
  ],
  theme: {
    extend: {},
  },
  plugins: [
    require('@tailwindcss/forms'),
  ],
}
