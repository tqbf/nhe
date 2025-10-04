module.exports = {
  content: [
    "./templates/**/*.html",
    "./demo.html",
  ],
  safelist: [
    'bg-blue-200',
    'bg-sky-200',
    'bg-cyan-200',
    'bg-teal-200',
    'bg-green-200',
    'bg-lime-200',
    'bg-yellow-200',
    'bg-amber-200',
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
