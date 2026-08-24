---
name: web-design
description: >
  Design and build modern web interfaces with strong semantics, accessibility, responsiveness, and security. Use for tasks involving web UI, layouts, components, styling, and frontend interaction patterns.
---

# Web Design Skill

## Purpose
Create modern, responsive, accessible, and secure web interfaces for contemporary websites and web apps.

This skill is optimized for:
- semantic HTML
- accessibility-first UI
- security-conscious implementation
- polished, playful, premium visuals
- fast, intuitive user experiences

## Core Design Vision
Build interfaces that feel:
- **Playful Glassmorphism UI** — soft translucent panels, vibrant accents, rounded shapes
- **Snap-Speed UX** — instant transitions, responsive interactions, minimal friction
- **Juicy Feedback** — satisfying animations, particles, sounds, and micro-interactions
- **Dynamic Depth** — layered UI, subtle parallax, shadows, and floating elements
- **Bold & Friendly Typography** — expressive headings with highly readable body text
- **Fluid Motion** — smooth, energetic transitions rather than static screens
- **Tactile Controls** — buttons and cards that feel responsive and physical
- **Clean Visual Hierarchy** — information stays simple despite the playful aesthetic
- **Living UI** — elements react to gameplay, player actions, and context
- **Premium Casual Feel** — polished enough to feel AAA without becoming overly serious
- **Colorful Minimalism** — strong colors used selectively against clean surfaces
- **Game-First Interface** — UI supports the action instead of competing with it

## Design Principles
1. **Clarity first**  
   Visual style must never reduce usability.

2. **Responsive by default**  
   Designs must work beautifully on mobile, tablet, and desktop.

3. **Accessible by default**  
   Every component must support keyboard, screen readers, contrast, and motion preferences.

4. **Semantic structure**  
   Use meaningful HTML elements before styling them.

5. **Secure by default**  
   Avoid unsafe patterns, insecure assumptions, and client-side trust.

6. **Delight with restraint**  
   Add motion and polish, but never at the cost of speed or readability.

## Visual Language
### Surfaces
- Use layered cards, panels, and modals with subtle blur, translucency, and depth.
- Prefer soft shadows and layered borders over harsh outlines.
- Use rounded corners consistently.

### Color
- Use a clean neutral base with selective vibrant accents.
- Reserve saturated colors for actions, highlights, status, and emotional emphasis.
- Maintain sufficient contrast for readability.

### Typography
- Headings: bold, expressive, compact, confident.
- Body: highly readable, comfortable line height, clear sizing.
- Avoid decorative type that harms legibility.

### Motion
- Use fast, smooth, meaningful transitions.
- Motion should clarify state changes, not distract.
- Respect `prefers-reduced-motion`.

### Shapes & Layout
- Prefer soft geometry, cards, floating panels, and layered sections.
- Keep layouts structured and easy to scan.
- Use spacing to create breathing room.

## UX Rules
- Make primary actions obvious.
- Reduce steps and cognitive load.
- Provide instant feedback on hover, focus, click, submit, and success/error states.
- Ensure empty states, loading states, and error states are designed.
- Avoid modal overload.
- Keep navigation predictable.
- Never hide essential information behind playful styling.

## Accessibility Rules
- Use semantic HTML elements:
  - `header`, `nav`, `main`, `section`, `article`, `aside`, `footer`
  - `button`, `a`, `form`, `label`, `input`, `select`, `textarea`
- All interactive elements must be keyboard accessible.
- All inputs must have labels.
- Use visible focus states.
- Ensure color contrast is sufficient.
- Do not rely on color alone to convey meaning.
- Provide text alternatives for icons/images.
- Support reduced motion.
- Announce dynamic updates where needed.
- Use logical heading order.

## Security Rules
- Never suggest insecure authentication or storage patterns.
- Do not expose secrets in client-side code.
- Sanitize user input before rendering.
- Escape content in templating and HTML output.
- Prefer server-side validation in addition to client-side validation.
- Avoid unsafe inline scripts when possible.
- Use secure defaults for forms, cookies, and sessions.
- Protect against common web threats:
  - XSS
  - CSRF
  - clickjacking
  - open redirects
  - insecure direct object references
- Never trust client-side state for authorization.

## Responsive Design Rules
- Design mobile-first.
- Use fluid layouts, flexible grids, and scalable typography.
- Ensure touch targets are large enough.
- Avoid hover-only interactions.
- Test breakpoints for:
  - small mobile
  - large mobile
  - tablet
  - desktop
  - wide desktop
- Content should reflow gracefully without layout breakage.

## Interaction Design
- Buttons should feel tactile and immediate.
- Use pressed, hover, focus, and disabled states.
- Add subtle motion for:
  - hover lift
  - click compression
  - panel reveal
  - list transitions
  - state changes
- Feedback should be immediate.
- Never block the UI without progress indication.

## Component Standards
Every component should have:
- clear purpose
- semantic markup
- accessible interaction
- responsive behavior
- loading state
- empty state
- error state
- disabled state if applicable

### Common Components
- Navigation bars
- Hero sections
- Feature cards
- Tabs
- Modals
- Drawers
- Toasts
- Forms
- Data tables
- Dashboards
- Pricing sections
- Testimonials
- FAQs
- Footer blocks

## Content Rules
- Write concise, scannable copy.
- Use plain language.
- Prefer action verbs.
- Avoid filler.
- Keep labels short and specific.
- Make microcopy helpful and reassuring.

## Implementation Preferences
When generating code or UI specs:
- Use semantic HTML.
- Prefer modern CSS architecture.
- Prefer reusable components.
- Keep styles modular and maintainable.
- Use performant animations.
- Avoid excessive DOM complexity.
- Avoid unnecessary JavaScript.
- Use progressive enhancement.

## Output Requirements
When asked to design or build a web interface, produce:
1. layout structure
2. visual style direction
3. responsive behavior
4. accessibility considerations
5. security considerations
6. component list
7. implementation-ready code or spec if requested

## Decision Filter
Before finalizing any design, ask:
- Is it clear?
- Is it accessible?
- Is it responsive?
- Is it secure?
- Is it fast?
- Is it delightful without becoming noisy?

If any answer is no, revise the design.

## Non-Negotiables
- No broken semantics
- No inaccessible controls
- No low-contrast text
- No motion that ignores user preferences
- No insecure patterns
- No cluttered layouts
- No decoration that harms usability

## Example Tone for Generated UI
“Modern, lively, and premium — like a polished game interface blended with a sleek product dashboard.”

## Final Rule
Make the interface feel alive, fast, and friendly — but always clear, semantic, accessible, and secure.
